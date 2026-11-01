# Protobuf generation

`pnpm gen-types` from `osac-ui/` retains the published stable API baseline selected
by `.buf-api-version`. Do not invent release tags or edit generated bindings.

For this coordinated Subnet API change, run `pnpm gen-types --local`. This explicit
mode generates the selected published baseline, then overlays only the Subnet
types named in `buf.local.gen.yaml` from the monorepo's `proto/` workspace. It uses
the pinned local `protoc-gen-es` dependency at the same version as the published
generator. Other bindings and the stable baseline remain unchanged. First run
`make -C proto generate` from the repository root to regenerate public sources.

The generated-types CI workflow runs the same explicit mode and checks for stale
or untracked bindings. Commit source, generated public protobufs, Go bindings,
and TypeScript bindings together. Once a real selected API release contains this
contract, retire the Subnet overlay in a coordinated change.

Run `pnpm i18n`, `pnpm run typecheck`, `pnpm lint`, and `pnpm test` after changes.
Translation extraction uses CI mode: the initial update may return nonzero;
rerun to verify synchronization.
