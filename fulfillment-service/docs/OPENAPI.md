# OpenAPI Documentation Publishing

The hosted OpenAPI docs (GitHub Pages) reflect the latest successfully
deployed output generated from the tip of `main`. They are **not**
release-versioned — each deployment replaces the entire site artifact, so no
per-version snapshot is retained across runs.

## Why no tag trigger?

Unlike sibling workflows (`publish-charts`, `publish-proto`, `publish-image`),
`publish-openapi.yaml` intentionally does **not** trigger on `v*` tag pushes:

1. **Redundant in the common case** — releases are cut from the tip of `main`,
   so by the time a tag is pushed the preceding `main` push has already
   published that exact content.

2. **Harmful in edge cases** — if a tag is ever cut from a commit that is not
   the current tip of `main` (release lag, hotfix), a tag-triggered run would
   republish older docs and overwrite newer content already live on the site
   until the next `main` push corrects it.

3. **Versioned publishing is not feasible** with the current GitHub Pages
   Actions-based deployment model, which replaces the whole site artifact on
   every run with no built-in mechanism to retain prior versions.

## Implications for consumers

Do not assume the hosted OpenAPI spec matches any specific tagged release.
To inspect the API surface for a particular version, check out that tag
locally and run:

```bash
go build ./cmd/osac-dev
./osac-dev generate openapi --project-dir . --output-dir pages/openapi
```
