# Examples

Each `*.tmpl` file is a self-contained example; the header comment shows the
command to render it. The AWS examples expect matching SSM parameters / secrets
and credentials, so they will fail unless those exist in your account — use
`--dry-run` to see the structure without resolving real values, or `--validate`
to check that every placeholder resolves.

| File | Shows |
| --- | --- |
| [`local.conf.tmpl`](local.conf.tmpl) | env / file / cmd sources, `default`/`required` and Sprig (`upper`) modifiers — renders with no AWS access |
| [`config.yml.tmpl`](config.yml.tmpl) | SSM, Secrets Manager, env, file and cmd sources; `default`/`required` and Sprig (`b64enc`) modifiers; per-environment paths |
| [`secrets.env.tmpl`](secrets.env.tmpl) | Rendering a dotenv file, e.g. to seed a container at startup |
| [`multiregion.yml.tmpl`](multiregion.yml.tmpl) | Pinning a region per placeholder with `aws@REGION:`, including the European Sovereign Cloud |

Quick try (no AWS needed):

```bash
API_USER=alice tplr --source example/local.conf.tmpl --allow-exec
```

For the AWS examples, `--mask` keeps SSM/Secrets Manager values hidden in the
output while showing the non-secret values, so the result is safe to share.

## Loading the template from S3

The template itself can live in S3 (S3 is treated as a config store, not a
secret store); secrets are still resolved from SSM / Secrets Manager:

```bash
tplr --source s3://my-config-bucket/app/config.yml.tmpl \
     --dest config.yml --env prod --region eu-central-1
```
