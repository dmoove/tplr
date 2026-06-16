package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// NotFoundError marks a value that does not exist (an unset parameter, a missing
// secret or key, an absent object), as opposed to a transport or authorization
// failure. Callers can use IsNotFound to act only on genuinely-missing values.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

// NotFound builds a NotFoundError.
func NotFound(format string, a ...any) error {
	return &NotFoundError{Msg: fmt.Sprintf(format, a...)}
}

// IsNotFound reports whether err indicates an absent value rather than a
// transport or authorization failure.
func IsNotFound(err error) bool {
	var e *NotFoundError
	return errors.As(err, &e)
}

// Client wraps AWS SDK clients.
type ssmClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type secretsManagerClient interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type s3Client interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type Client struct {
	region string
	ssm    ssmClient
	secret secretsManagerClient
	s3     s3Client
}

// New initializes AWS SDK clients using the default config chain. If region is
// non-empty it overrides the region resolved from the environment/profile, which
// is required to target a non-standard partition such as the AWS European
// Sovereign Cloud (e.g. "eusc-de-east-1"). The SDK resolves the matching
// partition endpoints and signing automatically from the region. maxAttempts
// caps the number of SDK request attempts (retries + 1); values <= 0 keep the
// SDK default.
func New(ctx context.Context, region string, maxAttempts int) (*Client, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(region))
	}
	if maxAttempts > 0 {
		loadOpts = append(loadOpts, awsconfig.WithRetryMaxAttempts(maxAttempts))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		region: cfg.Region,
		ssm:    ssm.NewFromConfig(cfg),
		secret: secretsmanager.NewFromConfig(cfg),
		s3:     s3.NewFromConfig(cfg),
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
		var pnf *ssmtypes.ParameterNotFound
		if errors.As(err, &pnf) {
			return "", NotFound("parameter %s not found", name)
		}
		return "", c.wrap(err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", NotFound("parameter %s not found", name)
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
			return "", NotFound("secret %s not found", id)
		}
		return "", c.wrap(err)
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
		return "", NotFound("key %s not found in secret %s", key, id)
	}
	return v, nil
}

// S3 fetches the contents of an object identified by bucket and key.
func (c *Client) S3(ctx context.Context, bucket, key string) (string, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		var nsk *s3types.NoSuchKey
		var nsb *s3types.NoSuchBucket
		switch {
		case errors.As(err, &nsk):
			return "", NotFound("object s3://%s/%s not found", bucket, key)
		case errors.As(err, &nsb):
			return "", NotFound("bucket %s not found", bucket)
		}
		return "", c.wrap(err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return "", fmt.Errorf("read s3://%s/%s: %w", bucket, key, err)
	}
	return string(data), nil
}

// wrap adds a region/partition hint to credential-mismatch errors, which are the
// most common failure when the configured credentials belong to a different AWS
// partition than the targeted region (e.g. commercial credentials against the
// European Sovereign Cloud, or vice versa).
func (c *Client) wrap(err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if strings.Contains(code, "UnrecognizedClient") || strings.Contains(code, "InvalidClientTokenId") || code == "AccessDeniedException" {
			return fmt.Errorf("%w (region %q: check that the credentials belong to the same AWS partition as the region)", err, c.region)
		}
	}
	return err
}
