package template

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dmoove/tplr/internal/aws"
)

type stubAWS struct {
	ssm      map[string]string
	secret   map[string]string
	ssmErr   error // when set, SSM returns this (a non-NotFound failure)
	ssmCalls int
	secCalls int
}

func (s *stubAWS) SSM(ctx context.Context, name string) (string, error) {
	s.ssmCalls++
	if s.ssmErr != nil {
		return "", s.ssmErr
	}
	v, ok := s.ssm[name]
	if !ok {
		return "", aws.NotFound("parameter %s not found", name)
	}
	return v, nil
}

func (s *stubAWS) SecretsManager(ctx context.Context, id, key string) (string, error) {
	s.secCalls++
	v, ok := s.secret[id]
	if !ok {
		return "", aws.NotFound("secret %s not found", id)
	}
	if key == "" {
		return v, nil
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(v), &obj); err != nil {
		return "", err
	}
	val, ok := obj[key]
	if !ok {
		return "", aws.NotFound("key %s not found in secret %s", key, id)
	}
	return val, nil
}

func TestExecTmpl(t *testing.T) {
	out, err := execTmpl("/app/{{env | toLower}}/db", "DEV")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if want := "/app/dev/db"; out != want {
		t.Fatalf("want %s got %s", want, out)
	}
}

func TestProcessWithClient(t *testing.T) {
	client := &stubAWS{
		ssm:    map[string]string{"/path": "ssmval"},
		secret: map[string]string{"/secret": `{"Password":"p"}`},
	}
	input := "a={{aws:ssm:/path}},b={{aws:secretsmanager:/secret#Password}}"
	got, err := ProcessWithClient(context.Background(), input, Options{Env: "DEV"}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a=ssmval,b=p"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestProcessWithClientNestedPath(t *testing.T) {
	client := &stubAWS{
		ssm:    map[string]string{"app/dev/dbPassword": "ssmval"},
		secret: map[string]string{"app/dev/db": `{"Password":"p"}`},
	}
	input := "a={{aws:ssm:app/{{env | toLower}}/dbPassword}},b={{aws:secretsmanager:app/{{env | toLower}}/db#Password}}"
	got, err := ProcessWithClient(context.Background(), input, Options{Env: "DEV"}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a=ssmval,b=p"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestProcessWithClientLeavesUnknownPlaceholder(t *testing.T) {
	client := &stubAWS{}
	input := "x={{not:a:secret}},y={{env}}"
	got, err := ProcessWithClient(context.Background(), input, Options{Env: "DEV"}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "x={{not:a:secret}},y={{env}}"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestEnvSource(t *testing.T) {
	t.Setenv("MY_VAR", "hello")
	got, err := ProcessWithClient(context.Background(), "v={{env:MY_VAR}}", Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "v=hello"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestEnvSourceMissingFails(t *testing.T) {
	_, err := ProcessWithClient(context.Background(), "{{env:DEFINITELY_NOT_SET_VAR}}", Options{}, &stubAWS{})
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("filesecret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessWithClient(context.Background(), "v={{file:"+path+"}}", Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "v=filesecret"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestMissingSecretFails(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{}}
	_, err := ProcessWithClient(context.Background(), "{{aws:ssm:/missing}}", Options{}, client)
	if err == nil {
		t.Fatal("expected error for missing parameter")
	}
}

func TestIgnoreMissingLeavesPlaceholder(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{}}
	input := "a={{aws:ssm:/missing}}"
	got, err := ProcessWithClient(context.Background(), input, Options{IgnoreMissing: true}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a={{aws:ssm:/missing}}"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestDryRunMasks(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{"/path": "supersecret"}}
	got, err := ProcessWithClient(context.Background(), "a={{aws:ssm:/path}}", Options{DryRun: true}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a=***"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestCustomDelimiters(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{"/path": "val"}}
	input := "a=[[aws:ssm:/path]],b={{aws:ssm:/path}}"
	got, err := ProcessWithClient(context.Background(), input, Options{Left: "[[", Right: "]]"}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	// Only the [[ ]] placeholder is resolved; {{ }} is left untouched.
	if want := "a=val,b={{aws:ssm:/path}}"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestParseAWSRef(t *testing.T) {
	cases := []struct {
		in           string
		region, rest string
		ok           bool
	}{
		{"aws:ssm:/path", "", "ssm:/path", true},
		{"aws@eusc-de-east-1:ssm:/path", "eusc-de-east-1", "ssm:/path", true},
		{"aws@eu-central-1:secretsmanager:id#key", "eu-central-1", "secretsmanager:id#key", true},
		{"aws@:ssm:/path", "", "", false}, // empty region
		{"env:VAR", "", "", false},
		{"file:/x", "", "", false},
	}
	for _, c := range cases {
		region, rest, ok := parseAWSRef(c.in)
		if ok != c.ok || region != c.region || rest != c.rest {
			t.Errorf("parseAWSRef(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, region, rest, ok, c.region, c.rest, c.ok)
		}
	}
}

func TestInlineRegionSelectsClient(t *testing.T) {
	// Track which region each client was created for. The provider is called
	// concurrently, so guard the map.
	var mu sync.Mutex
	used := map[string]*stubAWS{}
	provider := func(region string) (AWSClient, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := used[region]; ok {
			return c, nil
		}
		c := &stubAWS{ssm: map[string]string{"/p": "val-" + region}}
		used[region] = c
		return c, nil
	}
	opts := Options{Region: "default-region"}
	input := "a={{aws:ssm:/p}},b={{aws@eusc-de-east-1:ssm:/p}}"
	got, err := process(context.Background(), input, opts, provider)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a=val-default-region,b=val-eusc-de-east-1"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
	if _, ok := used["default-region"]; !ok {
		t.Error("expected a client for the default region")
	}
	if _, ok := used["eusc-de-east-1"]; !ok {
		t.Error("expected a client for the inline region")
	}
}

func TestInlineRegionSecretsManager(t *testing.T) {
	client := &stubAWS{secret: map[string]string{"app/db": `{"Password":"p"}`}}
	got, err := ProcessWithClient(context.Background(),
		"{{aws@eusc-de-east-1:secretsmanager:app/db#Password}}", Options{}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "p"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}

func TestCachingDedupesLookups(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{"/path": "val"}}
	input := "{{aws:ssm:/path}}{{aws:ssm:/path}}{{aws:ssm:/path}}"
	got, err := ProcessWithClient(context.Background(), input, Options{}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "valvalval"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
	if client.ssmCalls != 1 {
		t.Fatalf("expected 1 SSM call, got %d", client.ssmCalls)
	}
}

func TestDefaultModifier(t *testing.T) {
	got, err := ProcessWithClient(context.Background(),
		"{{env:DEFINITELY_NOT_SET_VAR | default \"fallback\"}}", Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "fallback"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestRequiredModifierFails(t *testing.T) {
	// required overrides IgnoreMissing.
	_, err := ProcessWithClient(context.Background(),
		"{{env:DEFINITELY_NOT_SET_VAR | required}}", Options{IgnoreMissing: true}, &stubAWS{})
	if err == nil {
		t.Fatal("expected required modifier to fail on a missing value")
	}
}

func TestSprigModifier(t *testing.T) {
	t.Setenv("MY_VAR", "hello")
	got, err := ProcessWithClient(context.Background(),
		"{{env:MY_VAR | upper}}", Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "HELLO"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestReplaceModifier(t *testing.T) {
	t.Setenv("MY_VAR", "a-b-c")
	got, err := ProcessWithClient(context.Background(),
		"{{env:MY_VAR | replace \"-\" \"_\"}}", Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a_b_c"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestMaskOnlyMasksSecrets(t *testing.T) {
	t.Setenv("USER_NAME", "alice")
	client := &stubAWS{ssm: map[string]string{"/pw": "supersecret"}}
	got, err := ProcessWithClient(context.Background(),
		"user={{env:USER_NAME}},pw={{aws:ssm:/pw}}", Options{Mask: true}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "user=alice,pw=***"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestCmdDisabledByDefault(t *testing.T) {
	_, err := ProcessWithClient(context.Background(), "{{cmd:echo hi}}", Options{}, &stubAWS{})
	if err == nil {
		t.Fatal("expected cmd source to be disabled without AllowExec")
	}
}

func TestCmdAllowExec(t *testing.T) {
	got, err := ProcessWithClient(context.Background(), "{{cmd:echo hi}}", Options{AllowExec: true}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "hi"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestModifierWithNestedPathTemplate(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{"app/dev/db": "secret"}}
	got, err := ProcessWithClient(context.Background(),
		"{{aws:ssm:app/{{env | toLower}}/db | upper}}", Options{Env: "DEV"}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "SECRET"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestDefaultDoesNotSwallowRealError(t *testing.T) {
	// A transport/authorization failure (not a NotFound) must propagate even
	// when a default is present, so a broken lookup never yields a fallback.
	client := &stubAWS{ssmErr: errors.New("ExpiredTokenException")}
	_, err := ProcessWithClient(context.Background(),
		`{{aws:ssm:/x | default "fallback"}}`, Options{}, client)
	if err == nil {
		t.Fatal("expected real lookup error to propagate past default")
	}
	if got := err.Error(); !strings.Contains(got, "ExpiredToken") {
		t.Fatalf("expected underlying error, got %q", got)
	}
}

func TestDefaultAppliesOnNotFound(t *testing.T) {
	client := &stubAWS{ssm: map[string]string{}} // missing -> NotFound
	got, err := ProcessWithClient(context.Background(),
		`{{aws:ssm:/missing | default "fallback"}}`, Options{}, client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "fallback"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestCmdShellPipeline(t *testing.T) {
	got, err := ProcessWithClient(context.Background(),
		"{{cmd:echo hi | tr a-z A-Z}}", Options{AllowExec: true}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "HI"; got != want {
		t.Fatalf("want %q got %q (cmd pipeline must not be parsed as a modifier)", want, got)
	}
}

func TestModifierArgWithPipe(t *testing.T) {
	t.Setenv("MY_VAR", "a|b")
	got, err := ProcessWithClient(context.Background(),
		`{{env:MY_VAR | replace "|" "/"}}`, Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a/b"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

// stubSops replaces the package-level sopsDecrypt with a recording fake for the
// duration of a test, so the template layer can be exercised without real key
// material.
func stubSops(t *testing.T, fn func(path, key string) (string, error)) {
	t.Helper()
	orig := sopsDecrypt
	sopsDecrypt = fn
	t.Cleanup(func() { sopsDecrypt = orig })
}

func TestSopsSource(t *testing.T) {
	stubSops(t, func(path, key string) (string, error) {
		if path != "secrets.yaml" || key != "db.password" {
			t.Fatalf("unexpected call path=%q key=%q", path, key)
		}
		return "s3cr3t", nil
	})
	got, err := ProcessWithClient(context.Background(),
		"pw={{sops:secrets.yaml#db.password}}", Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "pw=s3cr3t"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestSopsSourceTemplatedPath(t *testing.T) {
	stubSops(t, func(path, key string) (string, error) {
		if path != "secrets/dev.yaml" {
			t.Fatalf("unexpected path %q", path)
		}
		return "v", nil
	})
	got, err := ProcessWithClient(context.Background(),
		"{{sops:secrets/{{env | toLower}}.yaml#k}}", Options{Env: "DEV"}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "v"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestSopsSourceIsMasked(t *testing.T) {
	stubSops(t, func(path, key string) (string, error) { return "topsecret", nil })
	got, err := ProcessWithClient(context.Background(),
		"{{sops:secrets.yaml#k}}", Options{Mask: true}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "***"; got != want {
		t.Fatalf("sops value should be masked: want %q got %q", want, got)
	}
}

func TestSopsMissingKeyDefaults(t *testing.T) {
	stubSops(t, func(path, key string) (string, error) {
		return "", aws.NotFound("key %s not found", key)
	})
	got, err := ProcessWithClient(context.Background(),
		`{{sops:secrets.yaml#absent | default "fallback"}}`, Options{}, &stubAWS{})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "fallback"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestSopsDecryptErrorPropagates(t *testing.T) {
	stubSops(t, func(path, key string) (string, error) {
		return "", errors.New("no matching keys found")
	})
	_, err := ProcessWithClient(context.Background(),
		`{{sops:secrets.yaml#k | default "fallback"}}`, Options{}, &stubAWS{})
	if err == nil {
		t.Fatal("expected decryption error to propagate past default")
	}
}
