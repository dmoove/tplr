package template

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"tplr/internal/aws"
)

// Default placeholder delimiters.
const (
	DefaultLeft  = "{{"
	DefaultRight = "}}"
)

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
	// cannot be resolved.
	IgnoreMissing bool
	// DryRun resolves placeholders (to verify they exist) but replaces every
	// resolved value with a mask so the output is safe to print or log.
	DryRun bool
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

// Process resolves placeholders using real AWS clients created via aws.New().
// Clients are created lazily and cached per region, so templates that reference
// only env and file sources work without AWS credentials being configured, and
// a per-placeholder region (aws@region:...) gets its own client.
func Process(ctx context.Context, input string, opts Options) (string, error) {
	clients := map[string]AWSClient{}
	provider := func(region string) (AWSClient, error) {
		if c, ok := clients[region]; ok {
			return c, nil
		}
		c, err := aws.New(ctx, region)
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
	cache       map[string]string
}

func process(ctx context.Context, input string, opts Options, awsProvider func(region string) (AWSClient, error)) (string, error) {
	p := &processor{
		ctx:         ctx,
		opts:        opts,
		awsProvider: awsProvider,
		cache:       map[string]string{},
	}
	left, right := opts.left(), opts.right()

	var b strings.Builder
	var errs []error
	for i := 0; i < len(input); {
		if strings.HasPrefix(input[i:], left) {
			if end, ok := matchPlaceholder(input[i:], left, right); ok {
				placeholder := input[i : i+end]
				inner := placeholder[len(left) : len(placeholder)-len(right)]
				val, handled, err := p.resolve(inner)
				i += end
				switch {
				case !handled:
					b.WriteString(placeholder)
				case err != nil:
					if opts.IgnoreMissing {
						b.WriteString(placeholder)
					} else {
						errs = append(errs, err)
					}
				case opts.DryRun:
					b.WriteString(mask(val))
				default:
					b.WriteString(val)
				}
				continue
			}
		}
		b.WriteByte(input[i])
		i++
	}
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return b.String(), nil
}

// resolve turns the inner content of a placeholder into its replacement value.
// handled is false when the placeholder is not a supported reference, in which
// case the caller keeps the original text.
func (p *processor) resolve(inner string) (value string, handled bool, err error) {
	if region, rest, ok := parseAWSRef(inner); ok {
		return p.resolveAWS(region, rest)
	}
	switch {
	case strings.HasPrefix(inner, "env:"):
		name, err := p.execTmpl(strings.TrimPrefix(inner, "env:"))
		if err != nil {
			return "", true, err
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", true, fmt.Errorf("environment variable %s not set", name)
		}
		return val, true, nil
	case strings.HasPrefix(inner, "file:"):
		path, err := p.execTmpl(strings.TrimPrefix(inner, "file:"))
		if err != nil {
			return "", true, err
		}
		return p.cached("file:"+path, func() (string, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("read file %s: %w", path, err)
			}
			return strings.TrimRight(string(data), "\n"), nil
		})
	default:
		return "", false, nil
	}
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
func (p *processor) resolveAWS(region, rest string) (string, bool, error) {
	if region == "" {
		region = p.opts.Region
	}
	switch {
	case strings.HasPrefix(rest, "ssm:"):
		path, err := p.execTmpl(strings.TrimPrefix(rest, "ssm:"))
		if err != nil {
			return "", true, err
		}
		return p.cached("ssm:"+region+":"+path, func() (string, error) {
			c, err := p.awsProvider(region)
			if err != nil {
				return "", fmt.Errorf("init aws: %w", err)
			}
			return c.SSM(p.ctx, path)
		})
	case strings.HasPrefix(rest, "secretsmanager:"):
		arg := strings.TrimPrefix(rest, "secretsmanager:")
		var key string
		if idx := strings.Index(arg, "#"); idx >= 0 {
			key = arg[idx+1:]
			arg = arg[:idx]
		}
		path, err := p.execTmpl(arg)
		if err != nil {
			return "", true, err
		}
		return p.cached("sm:"+region+":"+path+"#"+key, func() (string, error) {
			c, err := p.awsProvider(region)
			if err != nil {
				return "", fmt.Errorf("init aws: %w", err)
			}
			return c.SecretsManager(p.ctx, path, key)
		})
	default:
		return "", false, nil
	}
}

// cached memoizes the result of fn under key for the lifetime of the processor,
// so repeated references to the same value trigger only a single lookup.
func (p *processor) cached(key string, fn func() (string, error)) (string, bool, error) {
	if v, ok := p.cache[key]; ok {
		return v, true, nil
	}
	v, err := fn()
	if err != nil {
		return "", true, err
	}
	p.cache[key] = v
	return v, true, nil
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

// mask hides a resolved value so it can be shown in dry-run output without
// leaking the secret (or its length).
func mask(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

func execTmpl(tmplStr string, env string) (string, error) {
	t, err := template.New("path").Funcs(template.FuncMap{
		"toLower": strings.ToLower,
		"env":     func() string { return env },
	}).Parse(tmplStr)
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	err = t.Execute(buf, contextData{Env: env})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
