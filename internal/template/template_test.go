package template

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

type stubAWS struct {
	ssm    map[string]string
	secret map[string]string
}

func (s *stubAWS) SSM(ctx context.Context, name string) (string, error) {
	v, ok := s.ssm[name]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

func (s *stubAWS) SecretsManager(ctx context.Context, id, key string) (string, error) {
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
	got, err := ProcessWithClient(context.Background(), input, "DEV", client)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "a=ssmval,b=p"; got != want {
		t.Fatalf("want %s got %s", want, got)
	}
}
