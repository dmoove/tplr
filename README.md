# tplr

A command line tool that replaces secret placeholders in configuration files.

## Placeholders

| Form | Source |
| --- | --- |
| `{{aws:ssm:PARAM}}` | AWS SSM Parameter Store (decrypted) |
| `{{aws:secretsmanager:SECRET}}` | AWS Secrets Manager (raw string) |
| `{{aws:secretsmanager:SECRET#KEY}}` | AWS Secrets Manager (JSON, key extracted) |
| `{{aws@REGION:ssm:PARAM}}` | As above, but pinned to a specific AWS region |
| `{{aws@REGION:secretsmanager:SECRET#KEY}}` | As above, but pinned to a specific AWS region |
| `{{env:VAR}}` | Environment variable |
| `{{file:/path}}` | File contents (trailing newline trimmed) |

An AWS reference may pin a region inline with `aws@REGION:` (e.g.
`{{aws@eusc-de-east-1:ssm:/app/db}}`). The inline region overrides `--region` /
`$AWS_REGION` for that placeholder, so a single file can pull from several
regions or partitions at once. Without it, the global region is used.

The path portion of any placeholder can be templated using the function `env`
and the function `toLower`.

Example YAML:

```yaml
passwordSSM: {{aws:ssm:app/{{env | toLower}}/dbPassword}}
passwordSecretsManagerNoJSON: {{aws:secretsmanager:app/{{env | toLower}}/dbPassword}}
passwordSecretsManagerJSON: {{aws:secretsmanager:app/{{env | toLower}}/db#Password}}
apiUser: {{env:API_USER}}
tlsCert: {{file:/etc/tls/cert.pem}}
```

## Usage

```bash
tplr --source config.yml.tmpl --dest config.yml --env DEV
```

The processed file is printed to stdout if `--dest` is not provided.

### Flags

| Flag | Description |
| --- | --- |
| `--source`, `--file` | Template file or glob pattern (required) |
| `--dest`, `--out` | Output file (default: stdout) |
| `--out-dir` | Output directory when `--source` matches multiple files; the template extension (`.tmpl`/`.tpl`/`.template`) is stripped |
| `--in-place` | Overwrite each source file with its rendered output |
| `--env` | Environment name (defaults to `$ENV`) |
| `--region` | AWS region for SSM/Secrets Manager (defaults to `$AWS_REGION`); required to target a non-standard partition such as the European Sovereign Cloud |
| `--left`, `--right` | Placeholder delimiters (default `{{` and `}}`); custom delimiters must not contain `{` or `}` |
| `--ignore-missing` | Leave placeholders untouched instead of failing when they cannot be resolved |
| `--dry-run` | Resolve placeholders (to verify they exist) but mask the values and write nothing |
| `--version` | Print version and exit |

By default tplr exits non-zero if any placeholder cannot be resolved, so a
broken render never silently produces an incomplete config. Repeated references
to the same parameter/secret are looked up only once per run.

Supported sources are AWS SSM Parameter Store, AWS Secrets Manager, environment
variables and files. Templates that use only `env`/`file` sources do not require
AWS credentials.

### AWS regions and the European Sovereign Cloud

AWS lookups use the region from `--region`, `$AWS_REGION` or the active profile.
The AWS European Sovereign Cloud (ESC) is a separate partition with its own
endpoints (`*.amazonaws.eu`) and credentials, so its region must be selected
explicitly:

```bash
tplr --source config.yml.tmpl --dest config.yml --region eusc-de-east-1
# or: AWS_REGION=eusc-de-east-1 tplr --source config.yml.tmpl --dest config.yml
# or pin it per placeholder: {{aws@eusc-de-east-1:secretsmanager:app/db#Password}}
```

Make sure the configured credentials belong to the same partition; commercial
credentials are rejected by ESC (and vice versa) with
`UnrecognizedClientException`.

## Install

Homebrew (macOS/Linux):

```bash
brew install dmoove/tap/tplr
```

With the Go toolchain:

```bash
go install github.com/dmoove/tplr/cmd/tplr@latest
```

Or grab a prebuilt binary (`linux`/`darwin` for `amd64`/`arm64`, `windows`
`amd64`) from the [releases page](https://github.com/dmoove/tplr/releases). Each
release also ships an SPDX SBOM (`tplr-<version>-sbom.spdx.json`), and the
container images carry SBOM and provenance attestations.

## Build

Compile the binary for your platform:

```bash
go build -o tplr ./cmd/tplr            # build for current OS/arch
GOOS=linux GOARCH=amd64 go build -o tplr-linux ./cmd/tplr
GOOS=darwin GOARCH=arm64 go build -o tplr-darwin ./cmd/tplr
GOOS=windows GOARCH=amd64 go build -o tplr.exe ./cmd/tplr
```

## Container

Multi-arch images (`linux/amd64`, `linux/arm64`) are published on every release
to GitHub Container Registry and Docker Hub:

```bash
docker pull ghcr.io/dmoove/tplr:latest      # or a pinned version, e.g. :0.3.0
docker pull dmoove/tplr:latest
```

Or build it yourself:

```bash
docker build -t tplr:latest .
```

The image can be used as an init container to populate a volume with the
rendered configuration file, for example in Kubernetes.

## Tests

Run unit tests and vet the code:

```bash
go vet ./...
go test ./...
```

## Release

Releases are automated with
[release-please](https://github.com/googleapis/release-please). Every push to
`main` is analysed for [Conventional Commits](https://www.conventionalcommits.org)
(`feat:`, `fix:`, `feat!:`/`BREAKING CHANGE:` for major), and release-please
maintains a release pull request with the computed version bump and a generated
`CHANGELOG.md`.

Merging that release PR creates the git tag and GitHub release, and the same
workflow builds the cross-platform binaries and attaches them to the release.
The binary version (`tplr --version`) is taken from the tag.

Because release-please derives the version from commit messages, merges to
`main` should use Conventional Commit messages (e.g. squash-merge with a
`feat: ...` or `fix: ...` PR title).

Released versions and their changelog are available on the
[releases page](https://github.com/dmoove/tplr/releases).

