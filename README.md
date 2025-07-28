# tplr

A command line tool that replaces secret placeholders in configuration files.

Placeholders have the form `{{aws:ssm:PARAM}}` or `{{aws:secretsmanager:SECRET#KEY}}`.
The path portion can be templated using the variable `env` and the function `toLower`.

Example YAML:

```yaml
passwordSSM: {{aws:ssm:app/{{env | toLower}}/dbPassword}}
passwordSecertsManagerNoJSON: {{aws:secretsmanager:app/{{env | toLower}}/dbPassword}}
passwordSecertsManagerJSON: {{aws:secretsmanager:app/{{env | toLower}}/db#Password}}
```

Usage:

```bash
tplr --source config.yml.tmpl --dest config.yml --env DEV
```

The processed file is printed to stdout if `--dest` is not provided.

Currently AWS SSM Parameter Store and AWS Secrets Manager are supported.

## Build

Compile the binary for your platform:

```bash
go build -o tplr ./cmd/tplr            # build for current OS/arch
GOOS=linux GOARCH=amd64 go build -o tplr-linux ./cmd/tplr
GOOS=darwin GOARCH=amd64 go build -o tplr-darwin ./cmd/tplr
GOOS=windows GOARCH=amd64 go build -o tplr.exe ./cmd/tplr
```

## Container

Build a Docker image that can be used as an init container:

```bash
docker build -t tplr:latest .
```

You can then run it in Kubernetes to populate a volume with the rendered
configuration file.

## Tests

Run unit tests and vet the code:

```bash
go vet ./...
go test ./...
```

## Release

Tagged commits starting with `v` trigger a GitHub Actions workflow that builds
cross-platform binaries and attaches them to a GitHub release.

