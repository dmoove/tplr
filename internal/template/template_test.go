package template

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type stubAWS struct {
	ssm      map[string]string
	secret   map[string]string
	ssmCalls int
	secCalls int
}

func (s *stubAWS) SSM(ctx context.Context, name string) (string, error) {
	s.ssmCalls++
	v, ok := s.ssm[name]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

func (s *stubAWS) SecretsManager(ctx context.Context, id, key string) (string, error) {
	s.secCalls++
	v, ok := s.secret[id]
	if !ok {
		return "", fmt.Errorf("not found")
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
		return "", fmt.Errorf("key not found")
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
