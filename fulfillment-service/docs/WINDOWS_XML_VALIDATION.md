# Windows user-data XML validation

The [PRD](https://github.com/osac-project/enhancement-proposals/blob/2e686fafcbf138067be6d005bf5cdc229f49f5ed/enhancements/OSAC-6781-windows-user-data-secret-type/prd.md)
and [design](https://github.com/osac-project/enhancement-proposals/blob/2e686fafcbf138067be6d005bf5cdc229f49f5ed/enhancements/OSAC-6781-windows-user-data-secret-type/design.md)
require XML format validation without Microsoft schema or namespace validation.
For user-facing examples and restrictions, see
[Managing Secrets](../../docs/guides/secrets-guide.md#windows-user-data-windows-user-data).
This document explains the implementation and its validation boundaries.

## Accepted XML profile

[`isPureXMLNoDTD`](../internal/servers/user_data_validation.go) accepts one
complete UTF-8 XML 1.0 fifth-edition document, with these rules:

- An XML declaration is optional. If present, it must specify version `1.0`;
  any encoding declaration must identify UTF-8, case-insensitively.
- A leading UTF-8 BOM is accepted. Validation leaves all input bytes unchanged,
  including the BOM, whitespace, line endings, and character references.
- XML names, characters, attributes, references, comments, processing
  instructions, and CDATA must follow XML 1.0 syntax. Element names are
  matched as written, and duplicate attribute names are rejected as written.
- DTDs and other directives are rejected. Comments and CDATA remain allowed
  in their valid document positions. Only predefined entity references and
  valid numeric character references are accepted in text and attributes.
- Namespace rules are not enforced: unbound prefixes and multiple colons in
  names are accepted. There are no Windows-specific root, schema, or setting
  checks, and no extra depth or attribute-count limits.

The helper performs no network or file I/O and does not expand entities.
Acceptance establishes the supported XML format, not Windows usability.

## Why a dedicated parser

The design proposes supplementing Go's `encoding/xml.Decoder`. The decoder
rejects some legal XML 1.0 fifth-edition names, including names with multiple
colons and newer Unicode ranges. It also accepts forbidden literal characters
in comments and processing instructions, and replaces surrogate character
references before callers can validate them. Using it as a final gate would
both reject allowed input and accept prohibited input.

The lexical parser therefore validates the document directly, using only the
standard library and the [XML 1.0 fifth-edition grammar](https://www.w3.org/TR/2008/REC-xml-20081126/).
An iterative stack tracks open element names without building a DOM or using
recursive descent. Separate helpers check declarations, names, characters,
references, and markup. Errors contain typed reason codes, never input text
or parser tokens.

## Callers and lifecycle

For `WINDOWS_USER_DATA` Secret creation,
[`validateSecretData`](../internal/servers/private_secrets_server.go) requires
nonempty `data["userdata"]`. `validateWindowsUserDataSecret` then enforces the
65,536-byte limit before calling the parser. The count includes any BOM,
declaration, and whitespace; it is independent of JSON's base64 encoding.
Parser failures map to fixed `InvalidArgument` messages before storage.
Generic `USER_DATA` Secret creation does not use the XML parser.

The existing Windows ComputeInstance admission path uses the same XML helper
for inline data and for resolved generic `USER_DATA` Secret contents. The XML
helper itself imposes no byte limit; callers own size validation. Supporting
`WINDOWS_USER_DATA` Secret references is separate work tracked in
[OSAC-7713](https://redhat.atlassian.net/browse/OSAC-7713).

Windows Secret data and type updates are rejected, and the `HUB` backend is
unsupported. Metadata-only updates are supported. A known, separate update
path can write data supplied outside a metadata-only update mask; it is
tracked in [OSAC-7880](https://redhat.atlassian.net/browse/OSAC-7880). The
metadata-preservation test sends metadata only and does not cover that bypass.

## Test boundaries

The parser tests exercise the accepted and rejected XML profiles directly.
HTTP and Linux file-access observers include positive controls to verify that
the no-external-resolution checks can detect access. Handler tests call the
servers in-process with real PostgreSQL and mocked Vault; public-server tests
check mapping and delegation through a mocked private server. These tests do
not exercise the deployed transport.

The deployed [`it/it_windows_user_data_secret_test.go`](../it/it_windows_user_data_secret_test.go)
suite exercises CLI creation, description and deletion, authenticated public
Get byte preservation, size boundaries, and rejection without persistence.
It requires a running service built from the checkout; compilation alone does
not verify that boundary. Follow the prerequisites and commands in
[Fulfillment integration testing](../../docs/INTEGRATION-TESTING.md#fulfillment-service).
