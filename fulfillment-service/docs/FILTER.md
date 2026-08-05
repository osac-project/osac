# Filter expressions

The list operations of the API accept a `filter` parameter whose value is a
[CEL](https://cel.dev) expression. The server evaluates this expression against each candidate
object and returns only those for which the result is `true`.

Inside the expression the built-in variable `this` refers to the object being tested, and `now`
refers to the current date and time. Field names correspond to the protobuf field names of the
object type, so `this.metadata.name` accesses the name stored in the object's metadata, and
`this.spec.region` accesses the `region` field inside the `spec` message.

For implementation reasons only a subset of the CEL language is currently supported. The rest of
this document describes what is available.

## Variables

There are two predeclared variables.

`this` represents the object being evaluated. Its type matches the protobuf message of the
resource being listed, so the available fields depend on the specific API. Every object exposes
at least `this.id` and `this.metadata`.

`now` evaluates to the current date and time on the server. It can be compared to timestamp fields,
for example `this.metadata.creation_timestamp < now`.

## Fields

Fields are accessed with the usual dot notation. Top-level fields such as `this.id` map directly
to database columns, while fields nested inside protobuf messages such as `this.spec.region`
are resolved from the JSON representation stored in the database.

The metadata sub-object exposes the following fields:

`this.metadata.name` is the human-readable name of the object.

`this.metadata.creation_timestamp` is the time the object was created.

`this.metadata.deletion_timestamp` is the time the object was marked for deletion. It is `null`
when the object has not been deleted, so `this.metadata.deletion_timestamp != null` selects only
deleted objects.

`this.metadata.creator` is the identity that created the object. For example,
`this.metadata.creator == 'my_user'`.

`this.metadata.tenant` is the tenant that owns the object. For example,
`this.metadata.tenant == 'my_tenant'`.

`this.metadata.labels` is the map of labels attached to the object. You can access individual
label values with the index operator, for example `this.metadata.labels['env'] == 'production'`.
To check whether a label key exists use `'env' in this.metadata.labels`.

## Supported protobuf field types

The translator handles the following protobuf field kinds when they appear inside a JSON-stored
message: `bool`, `int32`, `int64`, `float`, `double`, `string`, and nested `message` types.
Timestamp fields (the well-known `google.protobuf.Timestamp` type) are treated as time values and
can be compared to `now` or to `null`.

## Operators

### Comparison

The operators `==`, `!=`, `>`, `>=`, `<` and `<=` work as expected. Comparing to `null` with `==`
or `!=` correctly tests for the absence or presence of a value, so
`this.metadata.deletion_timestamp == null` matches objects whose deletion timestamp is absent.

### Arithmetic

The operators `+`, `-`, `*`, `/` and `%` (modulo) are supported on numeric values.

### Logical

Use `&&` for logical _and_, `||` for logical _or_, and `!` for logical _not_. Parentheses can be
used to control evaluation order.

### Membership with `in`

The `in` operator can test membership in three different contexts.

When the right-hand side is a list literal the expression checks whether the left-hand value
appears in that list. For example, `this.id in ['abc', 'def']` returns `true` when the object
identifier is either `abc` or `def`. Note that the list must contain only literal values.

When the right-hand side is an array field, the expression checks whether the left-hand value is
contained in that array.

When the right-hand side is a map field such as `this.metadata.labels`, the expression checks
whether the left-hand value exists as a key in the map. For example,
`'env' in this.metadata.labels`.

### Map index

The index operator `[]` can be used to access values inside map fields. For example,
`this.metadata.labels['env']` returns the value of the `env` label. The key must be a literal
string. The result can then be compared with `==` or `!=`.

## String functions

Three string functions are supported, and they must be called on a string field with a single
literal string argument.

`this.metadata.name.startsWith("prod")` is `true` when the name starts with `prod`.

`this.metadata.name.endsWith("-v2")` is `true` when the name ends with `-v2`.

`this.metadata.name.contains("test")` is `true` when the name contains `test` anywhere.

## The `has` macro

The `has` macro tests whether a field is present in the object. For most fields it checks whether
the field exists in the stored JSON data. For example, `has(this.spec)` is `true` when the `spec`
field is present. The metadata fields `id`, `name` and `creation_timestamp` are always considered
present. The `deletion_timestamp` field is considered present only when the object has actually
been marked for deletion.

## Examples

Select objects whose name starts with `prod`:

```cel
this.metadata.name.startsWith("prod")
```

Select objects created by a specific user:

```cel
this.metadata.creator == 'my_user'
```

Select objects that have the label `env` set to `production`:

```cel
this.metadata.labels['env'] == 'production'
```

Select objects that have not been deleted:

```cel
this.metadata.deletion_timestamp == null
```

Combine multiple conditions:

```cel
this.metadata.name.startsWith("prod") && this.metadata.labels['tier'] == 'frontend'
```
