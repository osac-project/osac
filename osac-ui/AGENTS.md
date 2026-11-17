# osac-ui

Never edit this file except for minor corrections. Do not append new content.

## Critical Rules

- Use PatternFly 6 components, tokens and utilities; avoid routine custom CSS
  and inline styles except values that cannot be expressed in CSS.
- TypeScript is strict: no enums; use string unions/const maps and interfaces
  for public props. Use expression-style functions as enforced by ESLint,
  default exports for React components and named exports otherwise.
- Keep one meaningful component per file and pages focused on composition.
  Use accessible labels/roles, keyboard focus and async error announcements.
- Use `OsacForm`, not raw PatternFly `Form`. Formik callers use field components
  under `libs/ui-components/src/components/Form/`, built on `Form/base/`
  primitives; add a missing primitive there before building a field.
- Trim user text at the shared field boundary before writing to Formik; keep
  an untrimmed draft while typing. Never trim again in callers/validation/
  submit handlers or alter API strings, IDs, option or programmatic values.
- Use deep ESM imports for PatternFly icons/tokens and `lodash-es/<function>`;
  follow `eslint.config.js` restrictions. No `console.log`.
- Preserve authorization and proxy trust boundaries; no credentials in source,
  fixtures or logs. Insecure TLS flags are development-only.

## Dev Environment

From `osac-ui/`, use the pnpm version in `package.json`'s `packageManager`
(and its `engines` requirements), then `pnpm install`.
`FULFILLMENT_API_URL=<endpoint> pnpm dev` starts the Go proxy and Vite.
For protected reflection, use the cluster/OIDC setup in
[README.md](README.md#quick-start); there is no local Fulfillment mock server.

## Validation

From `osac-ui/`, before commit/push/PR for UI code:

```bash
pnpm run typecheck
pnpm lint
pnpm test
```

These are separate checks: Vitest and production builds do not replace
checking test-file types. Lint checks ESLint, Prettier and i18n synchronization.
Run `pnpm build` for build/config or cross-package import changes. For proxy
changes also run `make -C proxy test lint`. For container inputs run
`make image-build IMG=<registry>/osac-ui:<tag>` from `osac-ui/`; the Makefile
supplies the repository root context, not the component directory.

## Architecture

- Browser Connect JSON at `/api/fulfillment/*` passes through
  `proxy/bridge/connectjson.go` to native gRPC using reflection, not proxy stubs.
- `ApiProvider` holds the transport; obtain typed clients with `useApiFetch`.
  Reads use `useApiQuery`/`useApiQueryClient`, not direct TanStack equivalents;
  mutations may use `useMutation`. Register routes in `ApiRoute` and use
  `apiQueryKey`; unwrap `.items`/`.object` in hook `select`, not components.
  Lists use `filter`, `limit`, `offset`, `order`, not cursors. See
  [API query architecture](docs/api-query-arch.md).
- Use `@osac/ui-components/hooks/useTranslation`, not `react-i18next` directly.
  Use literal English keys (`t('Save')`), never dynamic keys; pure utilities
  receive `t: TFunction`. Run `pnpm i18n` and commit generated
  `libs/i18n/locales/en/translation.json` with source strings; never hand-edit it.
- `libs/types/src/` is generated. AI agents may advance the API baseline only
  on an explicit user request for a UI API-version bump; dedicated API-release
  automation may also bump it. When authorized, change
  `libs/types/.buf-api-version`, run `pnpm gen-types` and commit generated files
  together. Never regenerate merely to make unrelated code compile.

## Test Boundaries

- Vitest/React Testing Library use the app-frontend runner for app and shared
  component tests. Pass `createMockConnectTransport` through `ApiProvider`;
  do not mock fetch/REST endpoints. These are not deployed-service evidence.
- Committed browser journeys live in `apps/e2e-tests/`; inspect its README and
  configuration before selecting the deployed Playwright suite.
- `apps/playwright/` hosts browser projects and live manual verification. Keep
  throwaway specs in gitignored `apps/playwright/scratch/` and remove them
  after use. Do not add feature specs under its `src/` or count scratch runs
  as persisted regression coverage. See its [README](apps/playwright/README.md).
- Memoization requires measured production-profiler evidence, not correctness
  workarounds; prefer state locality and stable, narrow props first.
