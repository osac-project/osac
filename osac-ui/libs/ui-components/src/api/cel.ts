import { toSnakeCase } from '../utils/snakeCase';

declare const celFilter: unique symbol;

/** A CEL expression constructed for a particular resource by {@link cel}. */
export type CelFilter<T = unknown> = string & { readonly [celFilter]: T };

type CelLiteral = boolean | number | string;
type EmptyCelFilter = '';
type KnownKeys<T> = {
  [K in keyof T]: string extends K ? never : number extends K ? never : K;
}[keyof T];
type StringKeys<T> = Extract<KnownKeys<T>, string>;
type PreviousDepth = [never, 0, 1, 2, 3, 4, 5];

/** Dot-separated paths to fields of a resource that can be addressed by CEL. */
export type CelFieldPath<T, Depth extends number = 5> = Depth extends 0
  ? never
  : T extends readonly unknown[]
    ? never
    : T extends object
      ? {
          [K in StringKeys<T>]: NonNullable<T[K]> extends readonly unknown[]
            ? K
            : NonNullable<T[K]> extends object
              ? K | `${K}.${CelFieldPath<NonNullable<T[K]>, PreviousDepth[Depth]>}`
              : K;
        }[StringKeys<T>]
      : never;

type CelFieldValue<T, Path extends string> = Path extends `${infer Head}.${infer Tail}`
  ? Head extends keyof T
    ? CelFieldValue<NonNullable<T[Head]>, Tail>
    : never
  : Path extends keyof T
    ? NonNullable<T[Path]>
    : never;
type CelPredicate<T> =
  | CelFilter<T>
  | EmptyCelFilter
  | ((builder: CelBuilder<T>) => CelFilter<T> | EmptyCelFilter | undefined);
type OptionalCelPredicate<T> = CelPredicate<T> | undefined;

export interface CelBuilder<T> {
  /** Selects a generated TypeScript resource field and emits its snake_case CEL path. */
  field: <Path extends CelFieldPath<T>>(path: Path) => CelField<T, CelFieldValue<T, Path>>;
  /** Matches only when every predicate matches. Emits an empty filter when no predicates are supplied. */
  and: (...predicates: readonly OptionalCelPredicate<T>[]) => CelFilter<T> | EmptyCelFilter;
  /** Matches when any predicate matches. Emits `false` when no predicates are supplied. */
  or: (...predicates: readonly OptionalCelPredicate<T>[]) => CelFilter<T>;
  /** Parenthesizes an expression to preserve its precedence when composing filters. */
  group: (filter: CelFilter<T> | EmptyCelFilter) => CelFilter<T> | EmptyCelFilter;
}

const asFilter = <T = unknown>(expression: string): CelFilter<T> => expression as CelFilter<T>;

/** Escapes a string for inclusion in a quoted CEL string literal. */
export const escapeCelStringLiteral = (value: string): string =>
  value
    .replaceAll('\\', '\\\\')
    .replaceAll('"', '\\"')
    .replaceAll('\r', '\\r')
    .replaceAll('\n', '\\n');

const toCelLiteral = (value: CelLiteral): string => {
  if (typeof value === 'string') {
    return `"${escapeCelStringLiteral(value)}"`;
  }
  return String(value);
};

const resolvePredicates = <T>(
  builder: CelBuilder<T>,
  predicates: readonly OptionalCelPredicate<T>[],
): CelFilter<T>[] =>
  predicates
    .filter((predicate): predicate is CelPredicate<T> => predicate !== undefined)
    .map((predicate) => (typeof predicate === 'function' ? predicate(builder) : predicate))
    .filter((predicate): predicate is CelFilter<T> => predicate !== undefined && predicate !== '');

class CelField<Resource, Value> {
  public constructor(private readonly path: string) {}

  /** Matches when the field equals `value`. Emits `field == value`. */
  public equals = (value: Extract<Value, CelLiteral>): CelFilter<Resource> =>
    asFilter<Resource>(`${this.path} == ${toCelLiteral(value)}`);

  /** Matches when the field does not equal `value`. Emits `field != value`. */
  public notEquals = (value: Extract<Value, CelLiteral>): CelFilter<Resource> =>
    asFilter<Resource>(`${this.path} != ${toCelLiteral(value)}`);

  /** Matches when the scalar field is one of `values`. Emits `field in [values]`. */
  public isIn = (values: readonly Extract<Value, CelLiteral>[]): CelFilter<Resource> =>
    asFilter<Resource>(`${this.path} in [${values.map(toCelLiteral).join(', ')}]`);

  /** Matches when a string field contains `value`. Emits `field.contains(value)`. */
  public contains = (value: Value extends string ? string : never): CelFilter<Resource> =>
    asFilter<Resource>(`${this.path}.contains(${toCelLiteral(value)})`);

  /**
   * Matches when a repeated field contains `value`.
   * Emits CEL such as `"gpu" in this.spec.tags`.
   */
  public includes = (
    value: Value extends readonly unknown[] ? CelLiteral : never,
  ): CelFilter<Resource> => asFilter<Resource>(`${toCelLiteral(value)} in ${this.path}`);

  /**
   * Matches when a repeated field contains at least one of the supplied values.
   * Emits CEL such as `"gpu" in this.spec.tags || "fast" in this.spec.tags`.
   * Returns `false` when no values are supplied.
   */
  public includesAny = (
    values: Value extends readonly unknown[] ? readonly CelLiteral[] : never,
  ): CelFilter<Resource> => {
    if (values.length === 0) {
      return asFilter<Resource>('false');
    }
    if (values.length === 1) {
      return asFilter<Resource>(`${toCelLiteral(values[0])} in ${this.path}`);
    }
    return asFilter<Resource>(
      `(${values.map((value) => `${toCelLiteral(value)} in ${this.path}`).join(' || ')})`,
    );
  };

  /**
   * Matches when at least one element in an array field equals `value`.
   * Emits CEL such as `this.spec.tags.exists(item, item == "gpu")`.
   *
   * @deprecated Use {@link includes} instead — the `exists()` macro produces
   * comprehension AST nodes that some backends (including fulfillment-service)
   * do not support.
   */
  public someEquals = (
    value: Value extends readonly (infer Item)[] ? Extract<Item, CelLiteral> : never,
    variableName = 'item',
  ): CelFilter<Resource> =>
    asFilter<Resource>(
      `${this.path}.exists(${variableName}, ${variableName} == ${toCelLiteral(value)})`,
    );

  /**
   * Matches when at least one element in an array field equals any supplied value.
   * Emits CEL such as `this.spec.tags.exists(item, item == "gpu" || item == "fast")`.
   * Returns `false` when no values are supplied.
   *
   * @deprecated Use {@link includesAny} instead — the `exists()` macro produces
   * comprehension AST nodes that some backends (including fulfillment-service)
   * do not support.
   */
  public someEqualsAny = (
    values: Value extends readonly (infer Item)[] ? readonly Extract<Item, CelLiteral>[] : never,
    variableName = 'item',
  ): CelFilter<Resource> => {
    if (values.length === 0) {
      return asFilter<Resource>('false');
    }
    return asFilter<Resource>(
      `${this.path}.exists(${variableName}, ${values
        .map((value) => `${variableName} == ${toCelLiteral(value)}`)
        .join(' || ')})`,
    );
  };
}

/**
 * Constructs CEL list filters from resource field paths. Both the path and the
 * literal type are checked against the TypeScript type generated from protobuf.
 */
const createCelBuilder = <T>(): CelBuilder<T> => {
  const builder: CelBuilder<T> = {
    field: <Path extends CelFieldPath<T>>(path: Path): CelField<T, CelFieldValue<T, Path>> =>
      new CelField(`this.${toSnakeCase(path)}`),
    and: (...predicates) => {
      const expressions = resolvePredicates(builder, predicates);
      return asFilter(expressions.join(' && '));
    },
    or: (...predicates) => {
      const expressions = resolvePredicates(builder, predicates);
      return asFilter(expressions.length ? `(${expressions.join(' || ')})` : 'false');
    },
    group: (filter) => (filter === '' ? filter : asFilter(`(${filter})`)),
  };
  return builder;
};

/** Builds a CEL expression scoped to a generated resource type. */
export const cel = <T>(
  build: (filter: CelBuilder<T>) => CelFilter<T> | EmptyCelFilter | undefined,
): CelFilter<T> | undefined => {
  const expression = build(createCelBuilder<T>());
  return expression === undefined || expression === '' ? undefined : expression;
};
