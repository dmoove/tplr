package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Client wraps AWS SDK clients.
type Client struct {
	ssm    *ssm.Client
	secret *secretsmanager.Client
}

// New initializes AWS SDK clients using default config.
func New() (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, err
	}
	return &Client{
		ssm:    ssm.NewFromConfig(cfg),
		secret: secretsmanager.NewFromConfig(cfg),
	}, nil
}

// SSM fetches a parameter with decryption.
func (c *Client) SSM(ctx context.Context, name string) (string, error) {
	withDec := true
	out, err := c.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &name,
		WithDecryption: &withDec,
	})
	if err != nil {
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("parameter %s not found", name)
	}
	return *out.Parameter.Value, nil
}

// SecretsManager fetches secret. If key is provided, the secret value is expected
// to be JSON and the key is extracted.
func (c *Client) SecretsManager(ctx context.Context, id string, key string) (string, error) {
	out, err := c.secret.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &id})
	if err != nil {
		var rnf *smtypes.ResourceNotFoundException
		if errors.As(err, &rnf) {
			return "", fmt.Errorf("secret %s not found", id)
		}
		return "", err
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secret %s has no string value", id)
	}
	if key == "" {
		return *out.SecretString, nil
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(*out.SecretString), &obj); err != nil {
		return "", fmt.Errorf("secret %s is not valid JSON: %w", id, err)
	}
	v, ok := obj[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in secret %s", key, id)
	}
	return v, nil
}
