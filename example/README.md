# Examples

Each `*.tmpl` file is a self-contained example; the header comment shows the
command to render it. The AWS examples expect matching SSM parameters / secrets
and credentials, so they will fail unless those exist in your account — use
`--dry-run` to see the structure without resolving real values, or `--validate`
to check that every placeholder resolves.

| File | Shows |
| --- | --- |
| [`local.conf.tmpl`](local.conf.tmpl) | env / file / cmd sources (incl. a cmd shell pipeline), `default`/`required` and Sprig (`upper`) modifiers — renders with no AWS access |
| [`modifiers.yml.tmpl`](modifiers.yml.tmpl) | The value-modifier pipeline: Sprig functions (`lower`, `upper`, `replace`, `trim`, `quote`, `b64enc`) plus `default`/`required` |
| [`config.yml.tmpl`](config.yml.tmpl) | SSM, Secrets Manager, env, file and cmd sources; modifiers; per-environment paths |
| [`secrets.env.tmpl`](secrets.env.tmpl) | Rendering a dotenv file, e.g. to seed a container at startup |
| [`multiregion.yml.tmpl`](multiregion.yml.tmpl) | Pinning a region per placeholder with `aws@REGION:`, including the European Sovereign Cloud |
| [`helm-values.yaml.tmpl`](helm-values.yaml.tmpl) | Custom delimiters (`--left`/`--right`) so tplr coexists with `{{ }}` used by another tool |

## Quick try (no AWS needed)

```bash
API_USER=alice tplr --source example/local.conf.tmpl --allow-exec
```

```bash
APP_NAME=My-App RAW="  spaced  " TENANT=acme \
  tplr --source example/modifiers.yml.tmpl
```

## Recipes for each flag

```bash
# Render to a file instead of stdout.
tplr --source example/config.yml.tmpl --dest config.yml --env dev

# Verify every placeholder resolves, write nothing (OK/FAIL per file, non-zero on failure).
tplr --source example/config.yml.tmpl --env dev --validate

# Resolve and print, but mask secret-source values; non-secret values stay visible.
tplr --source example/config.yml.tmpl --env dev --mask

# Resolve to verify, but mask EVERY value and write nothing.
tplr --source example/config.yml.tmpl --env dev --dry-run

# Leave unresolved placeholders untouched instead of failing.
tplr --source example/config.yml.tmpl --env dev --ignore-missing

# Enable the cmd: source (runs shell commands from the template).
API_USER=alice tplr --source example/local.conf.tmpl --allow-exec

# Custom delimiters so {{ }} (e.g. Helm) is left alone.
tplr --source example/helm-values.yaml.tmpl --left '[[' --right ']]' --env dev

# Tune AWS behaviour: overall timeout, retries and lookup parallelism.
tplr --source example/config.yml.tmpl --env dev \
     --timeout 30s --retries 5 --concurrency 16

# Render many templates at once into an output directory (.tmpl extension stripped).
tplr --source 'example/*.tmpl' --out-dir rendered --env dev --ignore-missing

# Overwrite each source file in place with its rendered output.
tplr --source config.yml.tmpl --in-place --env dev

# Generate shell completion.
tplr completion bash > /etc/bash_completion.d/tplr
tplr completion zsh  > "${fpath[1]}/_tplr"
tplr completion fish > ~/.config/fish/completions/tplr.fish
```

## Loading the template from S3

The template itself can live in S3 (S3 is treated as a config store, not a
secret store); secrets are still resolved from SSM / Secrets Manager:

```bash
tplr --source s3://my-config-bucket/app/config.yml.tmpl \
     --dest config.yml --env prod --region eu-central-1
```
