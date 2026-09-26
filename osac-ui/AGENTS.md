# osac-ui

> See [README.md](README.md) for project overview and quick start. This file contains agent-specific development conventions and quality standards.

## Critical Rules

- **PatternFly 6** is the design system — prefer PatternFly components, tokens, and utilities over custom markup
- **TypeScript strict mode** — no enums, use string unions or const maps; prefer interfaces over type aliases for public props
- **One component per file** — keep page files focused on composition and data wiring; extract subcomponents
- **No inline styles** except for dynamic values that cannot be expressed in CSS
- **Default exports** for React components; **named exports** for everything else (utilities, hooks, types, constants)
- **No `console.log`** — ESLint enforces this
- **Arrow function style** — `func-style: expression` is enforced

## Dev Environment

**Stack**: pnpm 9+ monorepo, Node.js 20+, React 19, TypeScript 5.9, PatternFly 6, Connect/gRPC-Web (`@connectrpc/connect-web`), Go 1.25+ (proxy)

**Prerequisites**:
- pnpm must be installed globally (`npm install -g pnpm@9`) — not included by default
- A running [fulfillment-service](https://github.com/osac-project/fulfillment-service) gRPC endpoint (there is no local mock server)

**Setup**:
```bash
pnpm install
FULFILLMENT_API_URL=https://... pnpm dev  # Go proxy + Vite on :5173
```

**Key commands**:
- `pnpm run typecheck` — `tsc --noEmit` in every package (including `*.test.ts` / `*.test.tsx`); **separate CI job** from lint and test
- `pnpm lint` — ESLint + Prettier + i18n sync check (CI fails if out of sync); **does not typecheck**
- `pnpm test` — app-frontend Vitest tests, including ui-components tests via include globs; **does not substitute for typecheck**
- `pnpm build` — app-frontend production `tsc -b` (excludes test files) + Vite build + Go binary
- `pnpm format` — Auto-fix linting and formatting issues
- `make -C ../proto generate` — Regenerate backend and UI types from protobuf
- `pnpm i18n` — Extract t() keys to libs/i18n/locales/en/translation.json

### Pre-submit validation (CI parity)

CI runs **separate** workflows for typecheck, lint, test, and container build. Passing one step does **not** mean the others pass. Before commit, push, or opening/updating a PR, run the full sequence:

```bash
pnpm run typecheck
pnpm lint
pnpm test
```

Run `pnpm build` when the change touches build config, imports across package boundaries, or you want local confirmation before the container-build job.

**Common gap:** Vitest can pass while `pnpm run typecheck` fails — for example, invalid protobuf `create(...)` fixtures in `libs/ui-components/src/**/*.test.tsx`. Those test files are included in `@osac/ui-components`'s `tsc --noEmit`, but excluded from the app-frontend production build (`tsconfig.build.json`). Always run `pnpm run typecheck`; do not infer it from `pnpm test` or `pnpm lint` alone.

**Container**:
```bash
podman build -t osac:latest -f Containerfile .
podman run -p 8080:8080 -e FULFILLMENT_API_URL=https://... osac:latest
```
Multi-stage build images: `nodejs-22-minimal:9.8`, `go-toolset:1.25`, `ubi-minimal:9.5` (see Containerfile)

**Go proxy** (proxy/):
- chi router with OIDC auth + Connect JSON → gRPC bridge
- Required env: `FULFILLMENT_API_URL` (HTTP URL; proxy derives the gRPC target from it)
- Optional: `OIDC_CLIENT_ID`, `BASE_UI_URL`, TLS CA files, insecure flags (dev only)
- API path: `/api/fulfillment/*` — Connect protocol (JSON) from the browser, translated to native gRPC upstream via `proxy/bridge/connectjson.go`
- Development: `cd proxy && nodemon --watch 'proxy/**/*' --exec 'go run' main.go`

## Key Workspace Packages

| Package | Agent-Relevant Notes |
|---------|---------------------|
| `@osac/app-frontend` | React SPA — Connect transport, `ApiProvider`, routing |
| `@osac/ui-components` | Shared components consumed at source (no build) — typed gRPC hooks live here |
| `@osac/types` | Generated protobuf types and service descriptors — **never edit**, regenerate with `make -C ../proto generate` |
| `@osac/i18n` | Translation extraction — `locales/en/translation.json` is generated, not hand-edited |
| `@osac/playwright` | Playwright harness for manual verification against a **live deployed cluster** — not a CI suite, not persisted test coverage. See [Manual verification against a live cluster](#manual-verification-against-a-live-cluster) |

## Code Style

- **ESLint 9** + TypeScript-ESLint with strict rules
- **Prettier**: single quotes, trailing commas, 100 char print width, 2-space indent
- **Import sorting**: enforced by ESLint `sort-imports` + `import/order` (alphabetized, grouped)
  - Order: builtin/external → internal (@osac/*) → parent/sibling → object (assets)
  - React imports first in external group
- **Arrow functions**: `prefer-arrow-callback` + `func-style: expression`
- **Unused vars**: error with `^_` ignore pattern
- **No console.log** — ESLint errors on console usage

### TypeScript and React

- Use **TypeScript** with strict project settings; prefer **interfaces** over type aliases for public props; **avoid enums** — use string unions or const maps
- Prefer **functional components** and declarative patterns; use the `function` keyword for named pure helpers when it improves hoisting and stack traces
- **Default exports** for React components; **named exports** for everything else (utilities, hooks, types, constants)
- **One component per file**: split each meaningful component into its own file in the same feature area (e.g., `feature-name/SubView.tsx`); keep page files focused on composition, data wiring, and layout. Exception: a tiny non-exported helper may stay if the file remains short
- **Restricted imports** (ESLint enforces):
  - Use `OsacForm` wrapper (`libs/ui-components/src/components/Form/OsacForm.tsx`), not PF `Form` directly
  - Deep imports for PF icons/tokens (ESM): `@patternfly/react-icons/dist/esm/icons/<name>`, `@patternfly/react-tokens/dist/esm/<token-name>`
  - Deep imports for lodash-es: `lodash-es/<function>`
  - Use `@osac/ui-components/hooks/useTranslation`, never `react-i18next` directly
  - ui-components consumers: use `useApiQuery`, never `@tanstack/react-query` `useQuery` directly
  - ui-components consumers: use `useApiQueryClient` from `@osac/ui-components/api/use-api-query`, never `useQueryClient` directly
- Do not add dependencies without aligning with existing stack and license policy; prefer patterns already present in the target package

### Styling

1. Prefer PatternFly CSS classes and utility classes
2. Avoid custom CSS files for routine UI — stay within PatternFly's supported customization paths
3. Never replace PatternFly tokens with arbitrary colors, spacing, or typography
5. Avoid inline styles (`style={{ ... }}`) except for dynamic values that cannot be expressed in CSS

### UI and Accessibility

- Base UI on [PatternFly 6](https://www.patternfly.org/) — layout, components, tokens, and patterns. For OpenShift-aligned UIs, also follow [OpenShift Console STYLEGUIDE.md](https://github.com/openshift/console/blob/main/STYLEGUIDE.md)
- Prefer accessible queries in tests and implementations: labels, roles, names — avoid `data-testid` unless the team standard requires it
- Meet keyboard and screen-reader expectations (focus order, labels, live regions for async errors)

### React Performance and `memo`

Treat memoization as an optimization, not a correctness tool:
- Do not rely on `memo` to fix broken behavior — fix purity, state placement, or data flow first
- Add `memo` only when justified: the child re-renders often with referentially stable props and its render work is measurably expensive
- `memo` does nothing if props are always new (inline objects/arrays/functions) — prefer narrower props, `children` as JSX, and local state
- Validate with React DevTools Profiler on a production build — reject memoization PRs without evidence
- Prefer structural fixes (state locality, simpler props) before adding `memo`/`useCallback`/`useMemo`
- If the repo enables [React Compiler](https://react.dev/learn/react-compiler), prefer compiler-driven memoization over manual `memo`

## Internationalization (i18n)

The app uses [i18next](https://www.i18next.com/) + [react-i18next](https://react.i18next.com/) with English string keys (the English text itself is the key). Translation files are generated by scanning source code for `t()` calls and `<Trans>` components.

### Generated file — do not edit by hand

`libs/i18n/locales/en/translation.json` is the single source of truth for translations. It is generated by `i18next-cli` and must not be edited manually. Update it by modifying source strings and running:

```bash
pnpm i18n
```

Commit the updated `translation.json` alongside the source changes. `pnpm lint` runs the same check in CI mode (`--ci`) and **fails if the file is out of sync** with the source.

### Using translations in components

Always import `useTranslation` from `@osac/ui-components/hooks/useTranslation` — never directly from `react-i18next`. ESLint enforces this.

```tsx
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const MyComponent = () => {
  const { t } = useTranslation();
  return <h1>{t('Page title')}</h1>;
};
```

For pure (non-component) utilities that need translations, accept `t: TFunction` as a parameter (imported from `i18next`) and call `useTranslation` in the component that invokes them:

```ts
import type { TFunction } from 'i18next';

export const getLabels = (t: TFunction) => ({
  save: t('Save'),
  cancel: t('Cancel'),
});
```

### Rules

- **Good:** `t('This is OK', { someVar })` — hardcoded string key, extractable by the parser
- **Bad:** `t(usingAVariable)` — dynamic key, not extractable; the CI i18n check will not catch missing translations

### Translation files at runtime

`vite-plugin-static-copy` copies `libs/i18n/locales/` into the Vite build output (`dist/locales/`). The Go proxy serves these as static files. The i18next HTTP backend loads them at runtime from `/locales/{{lng}}/{{ns}}.json`.

## Test

**Unit tests** (Vitest + React Testing Library):
- Framework: Vitest 4.x with jsdom
- Run: `pnpm test` — executes `@osac/app-frontend` vitest, which also runs ui-components tests via include globs
- Per-package: `pnpm --filter @osac/app-frontend run test`
- Location: `*.{test,spec}.{ts,tsx}` alongside source in `apps/app-frontend/src/` and `libs/ui-components/src/`
- Example paths:
  - `libs/ui-components/src/VmStatusLabel.test.tsx`
  - `libs/ui-components/src/api/types.test.ts`
  - `libs/ui-components/src/components/Form/fieldError.test.ts`

**Mocking API calls in tests**:
- Use `createMockConnectTransport` from `libs/ui-components/src/components/catalogProvision/test/createMockConnectTransport.ts` — in-memory Connect router transport for protobuf services
- Pass the mock transport to `ApiProvider`; do not mock fetch or REST endpoints (the REST layer was removed)

**Test guidelines**:
- Assert what the user sees and does — prefer accessible queries (labels, roles, names)
- Cover happy path, loading, empty, and error states
- Prefer stable, user-facing selectors over brittle DOM structure
- No `data-testid` unless team standard requires it

**Test configuration**:
- `apps/app-frontend/vitest.config.ts` — single runner for app-frontend and ui-components tests (`include` spans both packages)
- ESLint relaxes type safety rules for test files (no-unsafe-* off)
- Testing libraries: @testing-library/react 16.x, @testing-library/jest-dom 6.x
- No persisted E2E test suite in this repo, and no persisted UI-level E2E coverage anywhere: `osac-test-infra`'s CI-tracked E2E suites hit the fulfillment gRPC/REST API and Kubernetes CRs directly — no browser, nothing UI-related
- CI runs typecheck, lint, test, and container build as **separate jobs**; run `pnpm run typecheck`, `pnpm lint`, and `pnpm test` locally before submitting (see [Pre-submit validation](#pre-submit-validation-ci-parity))

### Manual verification against a live cluster

`apps/playwright` (`@osac/playwright`) is a Playwright harness for manually confirming
that a change works end-to-end against a **live, already-deployed** cluster —
not a CI suite and not something that adds to this repo's persisted test count. See
[apps/playwright/README.md](apps/playwright/README.md).

- **This is not a test suite — specs are throwaway**: write ad hoc specs under
  `apps/playwright/scratch/`, a gitignored directory that only exists locally.
  Nothing there is ever committed, but gitignore doesn't stop it from running —
  `pnpm playwright` reruns every spec still in the directory, so delete a spec
  once you're done with it. Only the harness itself (`playwright.config.ts`,
  `auth.setup.ts`) plus one fixed exception, `src/smoke.spec.ts` (a persisted
  harness self-check — loads the app, asserts the masthead renders — not a
  feature test), is committed. Never write other specs under
  `apps/playwright/src/`, and never treat a `scratch/` spec as regression
  coverage.
- Iterating on a `scratch/` spec: `pnpm playwright:setup` logs in once, then
  `pnpm playwright:run` re-runs just the `scratch/` specs without
  re-authenticating — faster than `pnpm playwright`, which always re-logs in.
- Use this when asked to verify a UI change actually works in a browser against
  a real deployment — requires `OSAC_UI_BASE_URL`, `OSAC_USERNAME`, `OSAC_PASSWORD`
  (or `OSAC_UI_BASE_URL=http://localhost:5173` against a local `pnpm dev`
  instance). There is no mock `fulfillment-service`, so this cannot run
  hermetically or in CI.

## Build

**Frontend build** (apps/app-frontend):
- `pnpm --filter @osac/app-frontend run build`
- TypeScript compilation (`tsc -b tsconfig.build.json`)
- Vite bundling → `apps/app-frontend/dist/`
- vite-plugin-static-copy: copies libs/i18n/locales/ to dist/locales/

**Go proxy build** (proxy/):
```bash
cd proxy && go build -o osac-proxy .
```

**Multi-stage container** (Containerfile):
1. ubi9/nodejs-22-minimal: install pnpm deps
2. Build SPA → apps/app-frontend/dist
3. ubi9/go-toolset:1.25: build Go proxy binary
4. ubi9/ubi-minimal:9.5: copy binary + SPA dist → /app/public

**Full build** (root):
```bash
pnpm build  # Builds frontend + proxy binary
```

**Type generation**:
- libs/types uses the shared Buf CLI configuration to generate TS types and Connect service descriptors from protobuf
- Run `make -C ../proto generate` after proto changes
- Never edit libs/types/src/*.ts manually

**i18n build**:
- i18next-cli extracts t() calls from source
- Updates libs/i18n/locales/en/translation.json
- CI fails if file is out of sync (`pnpm run i18n` in lint step)

## Architecture (Agent Key Points)

**Stack** (from package manifests):
- UI: PatternFly 6, React 19, react-router-dom 7, TanStack Query 5
- API: Connect/gRPC-Web (`@connectrpc/connect`, `@connectrpc/connect-web`), protobuf types from `@osac/types`
- Forms: Formik + Yup in `libs/ui-components`

**Connect transport and API layer** — see [docs/api-query-arch.md](docs/api-query-arch.md):
- Browser sends Connect JSON to `/api/fulfillment` via `createConnectTransport` in `apps/app-frontend/src/main.tsx`
- Go proxy (`proxy/bridge/connectjson.go`) translates Connect JSON → native gRPC to fulfillment-service
- `ApiProvider` stores the Connect `Transport`; hooks get typed clients via `useApiFetch(ServiceDescriptor)` from `libs/ui-components/src/api/api-context.tsx`
- `connectErrorInterceptor` converts `ConnectError(Unauthenticated)` to `UnauthorizedError` for login redirect

**Resource hooks** (`libs/ui-components/src/api/v1/*`):
- Reads: `useApiQuery` with a `queryFn` that calls the Connect client (`client.list`, `client.get`, …)
- Writes: `useMutation` calling Connect client RPCs (`client.create`, `client.delete`, …)
- Cache keys: `apiQueryKey('v1/<resource>', …)` — register new routes in `ApiRoute` (`libs/ui-components/src/api/types.ts`) first
- List pagination: shared `ListParams` (`filter`, `limit`, `offset`, `order`) — not cursor-based
- Unwrap responses in hook `select` (`.items` / `.object`), not in components

**ESLint restrictions** (ui-components consumers):
- Use `useApiQuery` / `useApiQueryClient`, not TanStack hooks directly (exception: `libs/ui-components/src/api/use-api-query.ts` implements the wrappers)
- Mutation hooks may import `useMutation` from `@tanstack/react-query` directly

**i18n flow**:
- English text is the key (e.g., `t('Save changes')`)
- i18next-cli extracts to `libs/i18n/locales/en/translation.json` (generated, not hand-edited)
- Runtime: i18next-http-backend loads from `/locales/{{lng}}/{{ns}}.json`
- CI enforces sync: `pnpm run i18n --ci` (fails if out of date)

**Type generation**:
- Protobuf → TypeScript + service descriptors via the shared Buf CLI configuration
- Never edit libs/types/src/*.ts manually — regenerate with `make -C ../proto generate`

**Component organization** (libs/ui-components/src):
- `components/`: catalog, catalogProvision, Cluster, dashboard, Form, Page, Primitives, Resource, vm, shared
- `pages/`: admin, provider, tenant
- `api/`: api-context, types, use-api-query
  - `api/v1/`: domain hooks (compute-instance, cluster, networking, …)

## Quality Bar

- Match existing formatting, import order, file layout, and naming in the touched package
- No broad refactors unrelated to the current task; smallest diff that satisfies requirements
- Run the [pre-submit validation](#pre-submit-validation-ci-parity) sequence before considering work done; fix new violations you introduce

## Security

**Agent guidelines**:
- No secrets, tokens, or credentials in source or tests
- Use existing env/config patterns for sensitive data (see proxy/ env vars)
- Sanitize or escape user-controlled content per framework norms
- Validate inputs at trust boundaries
- Follow authz semantics from architecture docs — do not bypass checks
- gitleaks config: `.gitleaks.toml` (pre-commit hook integration via rh-pre-commit)

## PR Conventions

**Branch naming**: feature/<description> or fix/<ticket>

**Commit messages**:
- Sign-off required (DCO)
- Prefix with ticket if applicable (e.g., "OSAC-1886: add VM details page")
- Conventional commits style observed in git log (feat, fix, refactor)

**AI attribution** — use one trailer matching the tool that assisted (never `Co-Authored-By` for AI tools — Red Hat standard):
```
Assisted-by: Cursor <cursoragent@cursor.com>
```
```
Assisted-by: Claude Code <noreply@anthropic.com>
```

**PR review**:
- CI must pass: typecheck, lint (TS + Go), test, container build
- On main merge or tag: publish container image to ghcr.io
- On `v*` tags: publish Helm chart to GHCR

**Scope discipline**:
- Implement and test only what the task requires
- No drive-by features unrelated to the task
- No broad refactors unrelated to current work
- Smallest diff that satisfies requirements

**Code review expectations**:
- Match existing formatting, import order, file layout, naming
- Run the [pre-submit validation](#pre-submit-validation-ci-parity) sequence before submitting
- Fix new violations you introduce
- No secrets, tokens, or credentials in source
- Sanitize user-controlled content
- Follow authz semantics from architecture docs

## Go Proxy (Agent Notes)

**Connect JSON bridge** (`proxy/bridge/connectjson.go`):
- Accepts Connect protocol (JSON) from the browser at `/api/fulfillment/*`
- Uses gRPC server reflection to translate to native gRPC — no proto stubs in the proxy
- gRPC target derived from `FULFILLMENT_API_URL` via `config.FulfillmentGrpcTarget()`

**Code style** (golangci-lint config v2):
- Linters: errcheck, staticcheck, govet, ineffassign, unused, misspell, revive, exhaustive, noctx, bodyclose
- Formatters: gofmt, goimports (local-prefixes: github.com/osac/proxy)
- Test files: errcheck disabled

**Development**:
```bash
cd proxy && nodemon --watch 'proxy/**/*' --exec 'go run' main.go
```

See README.md for env vars and deployment details.

## Documentation Reference

| Document | Location |
|----------|----------|
| API query architecture (Connect/gRPC) | [docs/api-query-arch.md](docs/api-query-arch.md) |
| OpenShift deployment | [docs/deployment-openshift-guide.md](docs/deployment-openshift-guide.md) |
