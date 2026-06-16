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
| `{{cmd:COMMAND}}` | Standard output of a shell command (requires `--allow-exec`) |

An AWS reference may pin a region inline with `aws@REGION:` (e.g.
`{{aws@eusc-de-east-1:ssm:/app/db}}`). The inline region overrides `--region` /
`$AWS_REGION` for that placeholder, so a single file can pull from several
regions or partitions at once. Without it, the global region is used.

The `cmd:` source runs an arbitrary shell command (`sh -c`) and is therefore
disabled unless you pass `--allow-exec`.

### Modifiers

A placeholder may pipe its resolved value through one or more modifiers using
`|`. The full [Sprig](https://masterminds.github.io/sprig/) function library is
available (`upper`, `lower`, `trim`, `replace`, `b64enc`, `quote`, …), plus two
that react to a missing or empty value:

| Modifier | Effect |
| --- | --- |
| `default "VALUE"` | Use `VALUE` when the reference is missing or resolves to an empty string |
| `required` / `required "MESSAGE"` | Fail when the value is missing or empty, even with `--ignore-missing` |

```yaml
name: {{env:APP_NAME | default "myapp" | upper}}
token: {{aws:ssm:/app/token | required "token must be set"}}
```

The path portion of any placeholder can also be templated with the same function
set (the historical `env` and `toLower` helpers still work); `env` returns the
`--env` value.

Example YAML:

```yaml
passwordSSM: {{aws:ssm:app/{{env | toLower}}/dbPassword}}
passwordSecretsManagerNoJSON: {{aws:secretsmanager:app/{{env | toLower}}/dbPassword}}
passwordSecretsManagerJSON: {{aws:secretsmanager:app/{{env | toLower}}/db#Password}}
apiUser: {{env:API_USER}}
tlsCert: {{file:/etc/tls/cert.pem}}
gitSha: {{cmd:git rev-parse --short HEAD}}
```

## Usage

```bash
tplr --source config.yml.tmpl --dest config.yml --env DEV
```

The processed file is printed to stdout if `--dest` is not provided.

Runnable templates live in [`example/`](example/) — see
[`example/README.md`](example/README.md) for what each one demonstrates and how
to render it.

### Templates in S3

`--source` may point at an `s3://bucket/key` object. tplr downloads that template
and renders it locally, resolving secret placeholders from SSM / Secrets Manager
as usual. S3 is used as a configuration store (not a secret store), so only the
template is fetched from it:

```bash
tplr --source s3://my-config-bucket/app/config.yml.tmpl \
     --dest config.yml --env prod --region eu-central-1
```

`--in-place` and `--out-dir` are not available for an `s3://` source; use
`--dest` or stdout.

### Flags

| Flag | Description |
| --- | --- |
| `--source`, `--file` | Template file, glob pattern or `s3://bucket/key` URI (required) |
| `--dest`, `--out` | Output file (default: stdout) |
| `--out-dir` | Output directory when `--source` matches multiple files; the template extension (`.tmpl`/`.tpl`/`.template`) is stripped |
| `--in-place` | Overwrite each source file with its rendered output |
| `--env` | Environment name (defaults to `$ENV`) |
| `--region` | AWS region for SSM/Secrets Manager/S3 (defaults to `$AWS_REGION`); required to target a non-standard partition such as the European Sovereign Cloud |
| `--left`, `--right` | Placeholder delimiters (default `{{` and `}}`); custom delimiters must not contain `{` or `}` |
| `--ignore-missing` | Leave placeholders untouched instead of failing when they cannot be resolved |
| `--dry-run` | Resolve placeholders (to verify they exist) but mask *every* value and write nothing |
| `--mask` | Mask values from secret sources (SSM/Secrets Manager) in the output while writing non-secret values verbatim |
| `--validate` | Resolve every placeholder to verify it exists, report `OK`/`FAIL` per file and write nothing (exits non-zero on any failure) |
| `--allow-exec` | Enable the `cmd:` source, which runs arbitrary shell commands from the template |
| `--timeout` | Overall timeout for resolving placeholders (e.g. `30s`); `0` means no timeout |
| `--retries` | Maximum AWS SDK retries per request (`0` uses the SDK default) |
| `--concurrency` | Maximum number of placeholder lookups resolved in parallel (default `8`) |
| `--version` | Print version and exit |

By default tplr exits non-zero if any placeholder cannot be resolved, so a
broken render never silently produces an incomplete config. Repeated references
to the same parameter/secret are looked up only once per run, and independent
lookups are resolved in parallel.

Supported sources are AWS SSM Parameter Store, AWS Secrets Manager, AWS S3,
environment variables, files and (with `--allow-exec`) shell commands. Templates
that use only `env`/`file`/`cmd` sources do not require AWS credentials.

### Shell completion

Generate a completion script for your shell:

```bash
tplr completion bash > /etc/bash_completion.d/tplr
tplr completion zsh  > "${fpath[1]}/_tplr"
tplr completion fish > ~/.config/fish/completions/tplr.fish
```

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

