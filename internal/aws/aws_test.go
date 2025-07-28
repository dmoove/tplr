package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type stubSSM struct {
	params map[string]string
	err    error
}

func (s *stubSSM) GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	name := ""
	if input != nil && input.Name != nil {
		name = *input.Name
	}
	val, ok := s.params[name]
	if !ok {
		return &ssm.GetParameterOutput{Parameter: nil}, nil
	}
	v := val
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: &v}}, nil
}

type stubSecrets struct {
	secrets     map[string]string
	returnEmpty bool
	err         error
}

func (s *stubSecrets) GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	id := ""
	if input != nil && input.SecretId != nil {
		id = *input.SecretId
	}
	val, ok := s.secrets[id]
	if !ok {
		return nil, &smtypes.ResourceNotFoundException{}
	}
	if s.returnEmpty {
		return &secretsmanager.GetSecretValueOutput{SecretString: nil}, nil
	}
	v := val
	return &secretsmanager.GetSecretValueOutput{SecretString: &v}, nil
}

func TestSSM(t *testing.T) {
	c := &Client{ssm: &stubSSM{params: map[string]string{"/name": "val"}}}
	got, err := c.SSM(context.Background(), "/name")
	if err != nil {
		t.Fatalf("ssm: %v", err)
	}
	if got != "val" {
		t.Fatalf("want val got %s", got)
	}
}

func TestSSMNotFound(t *testing.T) {
	c := &Client{ssm: &stubSSM{}}
	_, err := c.SSM(context.Background(), "missing")
	if err == nil || err.Error() != "parameter missing not found" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSecretsManager(t *testing.T) {
	c := &Client{secret: &stubSecrets{secrets: map[string]string{"id": `{"Password":"p"}`}}}
	got, err := c.SecretsManager(context.Background(), "id", "Password")
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if got != "p" {
		t.Fatalf("want p got %s", got)
	}
}

func TestSecretsManagerNotFound(t *testing.T) {
	c := &Client{secret: &stubSecrets{}}
	_, err := c.SecretsManager(context.Background(), "notthere", "")
	if err == nil || err.Error() != "secret notthere not found" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSecretsManagerNoString(t *testing.T) {
	c := &Client{secret: &stubSecrets{secrets: map[string]string{"id": "ignored"}, returnEmpty: true}}
	_, err := c.SecretsManager(context.Background(), "id", "")
	if err == nil || err.Error() != "secret id has no string value" {
		t.Fatalf("unexpected error: %v", err)
	}
}
