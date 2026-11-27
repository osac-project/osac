# Windows user-data XML validation

The [PRD](https://github.com/osac-project/enhancement-proposals/blob/2e686fafcbf138067be6d005bf5cdc229f49f5ed/enhancements/OSAC-6781-windows-user-data-secret-type/prd.md)
and [design](https://github.com/osac-project/enhancement-proposals/blob/2e686fafcbf138067be6d005bf5cdc229f49f5ed/enhancements/OSAC-6781-windows-user-data-secret-type/design.md)
require XML format validation without Microsoft schema or namespace validation.
For user-facing examples and restrictions, see
[Managing Secrets](../../docs/guides/secrets-guide.md#windows-user-data-windows-user-data).
This document explains the implementation and its validation boundaries.

## Accepted XML profile

The [Secrets guide](../../docs/guides/secrets-guide.md#windows-user-data-windows-user-data)
defines the accepted XML profile, size limit, and Secret restrictions.
[`isPureXMLNoDTD`](../internal/servers/user_data_validation.go) validates the
XML syntax without changing the submitted bytes or performing external I/O.

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
[`validateSecretData`](../internal/servers/private_secrets_server.go) checks the
required data key, then `validateWindowsUserDataSecret` checks the size before
calling the parser. Generic `USER_DATA` Secret creation does not use it.

Windows ComputeInstance admission uses the same parser for inline data and
resolved `USER_DATA` Secret contents. Callers own size validation; the parser
itself has no byte limit. Support for `WINDOWS_USER_DATA` references follows in
[OSAC-7713](https://redhat.atlassian.net/browse/OSAC-7713).

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
