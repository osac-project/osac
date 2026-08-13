# Protocol Buffer Conventions

See [AGENTS.md § API Design Guidelines](../../AGENTS.md#api-design-guidelines) before adding or modifying any `.proto` file — [`docs/API.md`](../../docs/API.md) is the authoritative source for all API conventions (object structure, naming, services, request/response patterns, REST transcoding, enums, conditions, references, and documentation requirements).

OSAC follows [Kubernetes API conventions](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md) adapted for protobuf.

## Workflow

- Always run `buf lint` before committing proto changes
- Regenerate code by running `buf generate` after proto changes
- `SERVICE_SUFFIX` lint rule is intentionally excluded in `buf.yaml`
