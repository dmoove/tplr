package template

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/dmoove/tplr/internal/aws"
	"github.com/dmoove/tplr/internal/sops"
)

// Default placeholder delimiters.
const (
	DefaultLeft  = "{{"
	DefaultRight = "}}"
)

// DefaultConcurrency is the number of placeholder lookups resolved in parallel
// when Options.Concurrency is not set.
const DefaultConcurrency = 8

type contextData struct {
	Env string
}

// AWSClient describes the subset of the AWS helpers required by the template
// processor. It allows tests to inject a mock implementation.
type AWSClient interface {
	SSM(ctx context.Context, name string) (string, error)
	SecretsManager(ctx context.Context, id, key string) (string, error)
}

// Options controls how a template is processed.
type Options struct {
	// Env is exposed to path templates via the env function.
	Env string
	// Region overrides the AWS region used for SSM/Secrets Manager lookups.
	// Empty falls back to the SDK's default resolution (AWS_REGION, profile).
	// Set this to target a non-standard partition such as the AWS European
	// Sovereign Cloud (e.g. "eusc-de-east-1").
	Region string
	// Left and Right are the placeholder delimiters. Empty values fall back to
	// the defaults ("{{" and "}}"). Custom delimiters must not contain "{" or
	// "}" because path templating always uses Go's {{ }} syntax.
	Left  string
	Right string
	// IgnoreMissing leaves a placeholder untouched instead of failing when it
	// cannot be resolved. The required modifier overrides this.
	IgnoreMissing bool
	// DryRun resolves placeholders (to verify they exist) but replaces every
	// resolved value with a mask so the output is safe to print or log.
	DryRun bool
	// Mask replaces values that come from a secret source (SSM, Secrets
	// Manager) with a mask while still emitting non-secret values verbatim, so
	// the rendered output can be shared without leaking secrets.
	Mask bool
	// AllowExec enables the cmd: source. It is off by default because it runs
	// arbitrary commands from the template.
	AllowExec bool
	// MaxRetries caps the number of AWS SDK retries per request. Zero keeps the
	// SDK default.
	MaxRetries int
	// Concurrency bounds how many lookups run in parallel. Values <= 0 fall back
	// to DefaultConcurrency.
	Concurrency int
}

func (o Options) left() string {
	if o.Left == "" {
		return DefaultLeft
	}
	return o.Left
}

func (o Options) right() string {
	if o.Right == "" {
		return DefaultRight
	}
	return o.Right
}

func (o Options) concurrency() int {
	if o.Concurrency <= 0 {
		return DefaultConcurrency
	}
	return o.Concurrency
}

// Process resolves placeholders using real AWS clients created via aws.New().
// Clients are created lazily and cached per region, so templates that reference
// only env, file and cmd sources work without AWS credentials being configured,
// and a per-placeholder region (aws@region:...) gets its own client.
func Process(ctx context.Context, input string, opts Options) (string, error) {
	var mu sync.Mutex
	clients := map[string]AWSClient{}
	provider := func(region string) (AWSClient, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := clients[region]; ok {
			return c, nil
		}
		c, err := aws.New(ctx, region, opts.MaxRetries)
		if err != nil {
			return nil, err
		}
		clients[region] = c
		return c, nil
	}
	return process(ctx, input, opts, provider)
}

// ProcessWithClient is like Process but uses the provided AWS client for every
// region. This is primarily intended for tests.
func ProcessWithClient(ctx context.Context, input string, opts Options, awsClient AWSClient) (string, error) {
	return process(ctx, input, opts, func(string) (AWSClient, error) { return awsClient, nil })
}

type processor struct {
	ctx         context.Context
	opts        Options
	awsProvider func(region string) (AWSClient, error)
	funcs       template.FuncMap
}

// token is a piece of the parsed input: either literal text or a placeholder.
type token struct {
	text string // literal text when ref == nil
	ref  *placeholder
}

// placeholder is a parsed "{{ spec | mod | mod }}" occurrence.
type placeholder struct {
	raw  string     // original text including delimiters, kept when unresolved
	spec string     // the reference part before the first top-level pipe
	mods []modifier // value modifiers applied after resolution
}

// baseResult is the outcome of resolving a placeholder's reference (before
// modifiers are applied).
type baseResult struct {
	value    string
	isSecret bool
	handled  bool
	err      error
}

func process(ctx context.Context, input string, opts Options, awsProvider func(region string) (AWSClient, error)) (string, error) {
	p := &processor{ctx: ctx, opts: opts, awsProvider: awsProvider, funcs: funcs(opts.Env)}
	tokens := p.parse(input)

	// Collect the unique reference specs that need resolving and resolve them
	// concurrently. Deduplicating by spec means repeated references trigger a
	// single lookup regardless of their (cheap) modifier chains.
	specs := map[string]struct{}{}
	for _, t := range tokens {
		if t.ref != nil {
			specs[t.ref.spec] = struct{}{}
		}
	}
	results := p.resolveAll(specs)

	var b strings.Builder
	var errs []error
	for _, t := range tokens {
		if t.ref == nil {
			b.WriteString(t.text)
			continue
		}
		ph := t.ref
		br := results[ph.spec]
		if !br.handled {
			b.WriteString(ph.raw)
			continue
		}
		val, err := p.applyModifiers(br.value, br.err, ph.mods)
		switch {
		case err != nil:
			var reqErr *requiredError
			if errors.As(err, &reqErr) {
				errs = append(errs, err)
			} else if opts.IgnoreMissing {
				b.WriteString(ph.raw)
			} else {
				errs = append(errs, err)
			}
		case opts.DryRun:
			b.WriteString(mask(val))
		case opts.Mask && br.isSecret:
			b.WriteString(mask(val))
		default:
			b.WriteString(val)
		}
	}
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return b.String(), nil
}

// resolveAll resolves every spec concurrently, bounded by Options.Concurrency.
func (p *processor) resolveAll(specs map[string]struct{}) map[string]baseResult {
	results := make(map[string]baseResult, len(specs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.opts.concurrency())
	for spec := range specs {
		wg.Add(1)
		go func(spec string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := p.resolveBase(spec)
			mu.Lock()
			results[spec] = r
			mu.Unlock()
		}(spec)
	}
	wg.Wait()
	return results
}

// parse splits the input into literal and placeholder tokens.
func (p *processor) parse(input string) []token {
	left, right := p.opts.left(), p.opts.right()
	var tokens []token
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			tokens = append(tokens, token{text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(input); {
		if strings.HasPrefix(input[i:], left) {
			if end, ok := matchPlaceholder(input[i:], left, right); ok {
				raw := input[i : i+end]
				inner := raw[len(left) : len(raw)-len(right)]
				spec, mods := parsePlaceholder(inner)
				flush()
				tokens = append(tokens, token{ref: &placeholder{raw: raw, spec: spec, mods: mods}})
				i += end
				continue
			}
		}
		lit.WriteByte(input[i])
		i++
	}
	flush()
	return tokens
}

// resolveBase turns a reference spec into its replacement value. handled is false
// when the spec is not a supported reference, in which case the caller keeps the
// original placeholder text.
func (p *processor) resolveBase(spec string) baseResult {
	if region, rest, ok := parseAWSRef(spec); ok {
		return p.resolveAWS(region, rest)
	}
	switch {
	case strings.HasPrefix(spec, "env:"):
		name, err := p.execTmpl(strings.TrimPrefix(spec, "env:"))
		if err != nil {
			return baseResult{handled: true, err: err}
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return baseResult{handled: true, err: aws.NotFound("environment variable %s not set", name)}
		}
		return baseResult{value: val, handled: true}
	case strings.HasPrefix(spec, "file:"):
		path, err := p.execTmpl(strings.TrimPrefix(spec, "file:"))
		if err != nil {
			return baseResult{handled: true, err: err}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return baseResult{handled: true, err: aws.NotFound("file %s not found", path)}
			}
			return baseResult{handled: true, err: fmt.Errorf("read file %s: %w", path, err)}
		}
		return baseResult{value: strings.TrimRight(string(data), "\n"), handled: true}
	case strings.HasPrefix(spec, "sops:"):
		return p.resolveSops(strings.TrimPrefix(spec, "sops:"))
	case strings.HasPrefix(spec, "cmd:"):
		return p.resolveCmd(strings.TrimPrefix(spec, "cmd:"))
	default:
		return baseResult{handled: false}
	}
}

// sopsDecrypt is the SOPS decryption entry point, indirected through a variable
// so tests can stub it without real key material.
var sopsDecrypt = sops.DecryptFile

// resolveSops decrypts a SOPS file reference. The argument is a path with an
// optional "#key" suffix selecting a dotted key path inside the decrypted
// document (e.g. sops:secrets.yaml#db.password). The path is templated like the
// other path-based sources; the key is taken verbatim.
func (p *processor) resolveSops(arg string) baseResult {
	var key string
	if idx := strings.Index(arg, "#"); idx >= 0 {
		key = arg[idx+1:]
		arg = arg[:idx]
	}
	path, err := p.execTmpl(arg)
	if err != nil {
		return baseResult{handled: true, isSecret: true, err: err}
	}
	v, err := sopsDecrypt(path, key)
	return baseResult{value: v, handled: true, isSecret: true, err: err}
}

func (p *processor) resolveCmd(arg string) baseResult {
	command, err := p.execTmpl(arg)
	if err != nil {
		return baseResult{handled: true, err: err}
	}
	if !p.opts.AllowExec {
		return baseResult{handled: true, err: fmt.Errorf("cmd source is disabled; pass --allow-exec to enable it")}
	}
	cmd := exec.CommandContext(p.ctx, "sh", "-c", command)
	out, err := cmd.Output()
	if err != nil {
		return baseResult{handled: true, err: fmt.Errorf("run command %q: %w", command, err)}
	}
	return baseResult{value: strings.TrimRight(string(out), "\n"), handled: true}
}

// parseAWSRef recognizes the AWS provider prefix with an optional inline region,
// i.e. "aws:<rest>" or "aws@<region>:<rest>". rest is the part after the
// provider (e.g. "ssm:/path" or "secretsmanager:id#key"). ok is false for any
// non-AWS reference.
func parseAWSRef(inner string) (region, rest string, ok bool) {
	if strings.HasPrefix(inner, "aws@") {
		after := inner[len("aws@"):]
		idx := strings.Index(after, ":")
		if idx <= 0 {
			return "", "", false
		}
		return after[:idx], after[idx+1:], true
	}
	if strings.HasPrefix(inner, "aws:") {
		return "", strings.TrimPrefix(inner, "aws:"), true
	}
	return "", "", false
}

// resolveAWS resolves an SSM or Secrets Manager reference. region is the inline
// region from the placeholder; when empty it falls back to Options.Region. An
// unsupported service is reported as not handled so the placeholder is kept.
func (p *processor) resolveAWS(region, rest string) baseResult {
	if region == "" {
		region = p.opts.Region
	}
	switch {
	case strings.HasPrefix(rest, "ssm:"):
		path, err := p.execTmpl(strings.TrimPrefix(rest, "ssm:"))
		if err != nil {
			return baseResult{handled: true, isSecret: true, err: err}
		}
		c, err := p.awsProvider(region)
		if err != nil {
			return baseResult{handled: true, isSecret: true, err: fmt.Errorf("init aws: %w", err)}
		}
		v, err := c.SSM(p.ctx, path)
		return baseResult{value: v, handled: true, isSecret: true, err: err}
	case strings.HasPrefix(rest, "secretsmanager:"):
		arg := strings.TrimPrefix(rest, "secretsmanager:")
		var key string
		if idx := strings.Index(arg, "#"); idx >= 0 {
			key = arg[idx+1:]
			arg = arg[:idx]
		}
		path, err := p.execTmpl(arg)
		if err != nil {
			return baseResult{handled: true, isSecret: true, err: err}
		}
		c, err := p.awsProvider(region)
		if err != nil {
			return baseResult{handled: true, isSecret: true, err: fmt.Errorf("init aws: %w", err)}
		}
		v, err := c.SecretsManager(p.ctx, path, key)
		return baseResult{value: v, handled: true, isSecret: true, err: err}
	default:
		return baseResult{handled: false}
	}
}

func (p *processor) execTmpl(tmplStr string) (string, error) {
	return execTmpl(tmplStr, p.opts.Env)
}

// matchPlaceholder returns the index just past the closing delimiter of the
// outermost placeholder that starts at the beginning of s. It tracks nesting so
// that inner "{{ ... }}" path templates are consumed as part of the surrounding
// placeholder. ok is false if no balanced closing exists.
func matchPlaceholder(s, left, right string) (int, bool) {
	depth := 0
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], left) {
			depth++
			i += len(left)
			continue
		}
		if strings.HasPrefix(s[i:], right) {
			depth--
			i += len(right)
			if depth == 0 {
				return i, true
			}
			continue
		}
		i++
	}
	return 0, false
}

// mask hides a resolved value so it can be shown in dry-run/mask output without
// leaking the secret (or its length).
func mask(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

func execTmpl(tmplStr string, env string) (string, error) {
	t, err := template.New("path").Funcs(funcs(env)).Parse(tmplStr)
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	if err := t.Execute(buf, contextData{Env: env}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// funcs is the function library available to both path templates and value
// modifiers. It is the full Sprig text/template catalog (upper, lower, trim,
// b64enc, replace, default, required, ...) plus tplr-specific helpers. tplr's
// no-argument env (which returns the --env value) intentionally shadows Sprig's
// env, and toLower/toUpper are kept as aliases for backwards compatibility with
// templates written before Sprig was integrated.
func funcs(env string) template.FuncMap {
	fm := sprig.TxtFuncMap()
	fm["env"] = func() string { return env }
	fm["toLower"] = strings.ToLower
	fm["toUpper"] = strings.ToUpper
	return fm
}

// requiredError is returned by the required modifier. It bypasses
// Options.IgnoreMissing so a required value can never be silently skipped.
type requiredError struct{ msg string }

func (e *requiredError) Error() string { return e.msg }

type modifier struct {
	name string
	args []string
}

// parsePlaceholder splits the inner content of a placeholder into the reference
// spec and its trailing modifiers. Splitting happens on top-level "|" only, so
// pipes inside nested "{{ ... }}" path templates or double-quoted modifier
// arguments are left untouched. A cmd: reference is exempt from splitting
// entirely, because "|" is a shell pipe operator and the whole content is the
// command; cmd: therefore takes no modifiers.
func parsePlaceholder(inner string) (spec string, mods []modifier) {
	if trimmed := strings.TrimSpace(inner); strings.HasPrefix(trimmed, "cmd:") {
		return trimmed, nil
	}
	parts := splitTopLevelPipe(inner)
	spec = strings.TrimSpace(parts[0])
	for _, raw := range parts[1:] {
		fields := tokenizeArgs(strings.TrimSpace(raw))
		if len(fields) == 0 {
			continue
		}
		mods = append(mods, modifier{name: fields[0], args: fields[1:]})
	}
	return spec, mods
}

// splitTopLevelPipe splits s on "|" characters that are not inside a nested
// "{{ ... }}" group or a double-quoted string.
func splitTopLevelPipe(s string) []string {
	var parts []string
	depth := 0
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			inQuote = !inQuote
		case inQuote:
			// Skip everything inside a quoted modifier argument.
		case strings.HasPrefix(s[i:], "{{"):
			depth++
			i++
		case strings.HasPrefix(s[i:], "}}"):
			if depth > 0 {
				depth--
			}
			i++
		case s[i] == '|' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// tokenizeArgs splits a modifier invocation into fields, honoring double-quoted
// segments so arguments may contain spaces.
func tokenizeArgs(s string) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false
	hasCur := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			hasCur = true
		case c == ' ' && !inQuote:
			if hasCur {
				fields = append(fields, cur.String())
				cur.Reset()
				hasCur = false
			}
		default:
			cur.WriteByte(c)
			hasCur = true
		}
	}
	if hasCur {
		fields = append(fields, cur.String())
	}
	return fields
}

// applyModifiers runs the modifier chain over the resolved value. default and
// required react only to a missing or empty value (a genuine "not found", not a
// transport/authorization failure) so a real lookup error is never silently
// replaced by a default; such an error stops the chain and propagates. Every
// other modifier is a Sprig/template function applied to a present value.
func (p *processor) applyModifiers(val string, err error, mods []modifier) (string, error) {
	for _, m := range mods {
		switch m.name {
		case "default":
			if isMissingOrEmpty(val, err) {
				if len(m.args) < 1 {
					return "", fmt.Errorf("default modifier requires an argument")
				}
				val, err = m.args[0], nil
			} else if err != nil {
				return "", err
			}
		case "required":
			if isMissingOrEmpty(val, err) {
				msg := "value is required but missing or empty"
				if len(m.args) >= 1 {
					msg = m.args[0]
				}
				return "", &requiredError{msg: msg}
			} else if err != nil {
				return "", err
			}
		default:
			if err != nil {
				return "", err
			}
			var terr error
			val, terr = p.transform(m, val)
			if terr != nil {
				return "", terr
			}
		}
	}
	return val, err
}

// isMissingOrEmpty reports whether the resolved value should be treated as
// absent: either the lookup reported a genuine "not found" or it succeeded with
// an empty string. A real (transport/authorization) error is not missing.
func isMissingOrEmpty(val string, err error) bool {
	if err != nil {
		return aws.IsNotFound(err)
	}
	return val == ""
}

// transform applies a single modifier by running the value through a Sprig
// pipeline (e.g. "{{ . | upper }}" or "{{ . | replace \"a\" \"b\" }}"), so the
// whole Sprig function catalog is available as a value modifier.
func (p *processor) transform(m modifier, val string) (string, error) {
	var sb strings.Builder
	sb.WriteString("{{ . | ")
	sb.WriteString(m.name)
	for _, a := range m.args {
		sb.WriteByte(' ')
		sb.WriteString(strconv.Quote(a))
	}
	sb.WriteString(" }}")

	t, err := template.New("mod").Funcs(p.funcs).Parse(sb.String())
	if err != nil {
		return "", fmt.Errorf("modifier %q: %w", m.name, err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, val); err != nil {
		return "", fmt.Errorf("modifier %q: %w", m.name, err)
	}
	return out.String(), nil
}
