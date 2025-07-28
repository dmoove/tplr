package template

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"tplr/internal/aws"
)

var placeholderRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

type contextData struct {
	Env string
}

// AWSClient describes the subset of the AWS helpers required by the template
// processor. It allows tests to inject a mock implementation.
type AWSClient interface {
	SSM(ctx context.Context, name string) (string, error)
	SecretsManager(ctx context.Context, id, key string) (string, error)
}

// Process fetches secrets referenced in the input template using a real AWS
// client created via aws.New().
func Process(ctx context.Context, input string, env string) (string, error) {
	awsClient, err := aws.New()
	if err != nil {
		return "", fmt.Errorf("init aws: %w", err)
	}
	return ProcessWithClient(ctx, input, env, awsClient)
}

// ProcessWithClient is like Process but uses the provided AWS client. This is
// primarily intended for tests.
func ProcessWithClient(ctx context.Context, input string, env string, awsClient AWSClient) (string, error) {
	result := placeholderRe.ReplaceAllStringFunc(input, func(s string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "{{"), "}}")
		if strings.HasPrefix(inner, "aws:ssm:") {
			pathTmpl := strings.TrimPrefix(inner, "aws:ssm:")
			path, err := execTmpl(pathTmpl, env)
			if err != nil {
				return fmt.Sprintf("{{ERROR:%v}}", err)
			}
			val, err := awsClient.SSM(ctx, path)
			if err != nil {
				return fmt.Sprintf("{{ERROR:%v}}", err)
			}
			return val
		}
		if strings.HasPrefix(inner, "aws:secretsmanager:") {
			arg := strings.TrimPrefix(inner, "aws:secretsmanager:")
			var key string
			if idx := strings.Index(arg, "#"); idx >= 0 {
				key = arg[idx+1:]
				arg = arg[:idx]
			}
			path, err := execTmpl(arg, env)
			if err != nil {
				return fmt.Sprintf("{{ERROR:%v}}", err)
			}
			val, err := awsClient.SecretsManager(ctx, path, key)
			if err != nil {
				return fmt.Sprintf("{{ERROR:%v}}", err)
			}
			return val
		}
		// not a supported placeholder
		return s
	})
	return result, nil
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
