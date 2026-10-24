import { describe, expect, expectTypeOf, it } from 'vitest';

import { type CelFilter, cel, escapeCelStringLiteral } from './cel';
import { type ListParams } from './types';

interface ExampleResource {
  id: string;
  metadata?: {
    name: string;
    tenant: string;
  };
  spec?: {
    enabled: boolean;
    guestOsFamily: number;
    replicas: number;
    tags: string[];
  };
}

describe('cel', () => {
  it('uses type-safe TypeScript paths and emits CEL field names', () => {
    expect(cel<ExampleResource>((filter) => filter.field('metadata.name').equals('worker'))).toBe(
      'this.metadata.name == "worker"',
    );
    expect(cel<ExampleResource>((filter) => filter.field('spec.enabled').equals(true))).toBe(
      'this.spec.enabled == true',
    );
  });

  it('escapes string literals and converts fields to snake_case', () => {
    expect(cel<ExampleResource>((filter) => filter.field('metadata.name').contains('a"b\\c'))).toBe(
      'this.metadata.name.contains("a\\"b\\\\c")',
    );
    expect(cel<ExampleResource>((filter) => filter.field('spec.guestOsFamily').equals(1))).toBe(
      'this.spec.guest_os_family == 1',
    );
  });

  it('escapes carriage returns and newlines in string literals', () => {
    expect(escapeCelStringLiteral('first\rsecond')).toBe('first\\rsecond');
    expect(escapeCelStringLiteral('first\nsecond')).toBe('first\\nsecond');
    expect(
      cel<ExampleResource>((filter) => filter.field('metadata.name').contains('first\r\nsecond')),
    ).toBe('this.metadata.name.contains("first\\r\\nsecond")');
  });

  it('composes predicates', () => {
    const filter = cel<ExampleResource>((builder) =>
      builder.and(
        builder.field('metadata.tenant').notEquals('shared'),
        builder.or(
          builder.field('spec.replicas').equals(1),
          builder.field('spec.replicas').equals(3),
        ),
      ),
    );

    expect(filter).toBe(
      'this.metadata.tenant != "shared" && (this.spec.replicas == 1 || this.spec.replicas == 3)',
    );
  });

  it('includes emits value-in-field for repeated fields', () => {
    expect(cel<ExampleResource>((filter) => filter.field('spec.tags').includes('gpu'))).toBe(
      '"gpu" in this.spec.tags',
    );
  });

  it('includesAny emits in-expressions joined by ||', () => {
    expect(cel<ExampleResource>((filter) => filter.field('spec.tags').includesAny(['gpu']))).toBe(
      '"gpu" in this.spec.tags',
    );
    expect(
      cel<ExampleResource>((filter) => filter.field('spec.tags').includesAny(['gpu', 'fast'])),
    ).toBe('("gpu" in this.spec.tags || "fast" in this.spec.tags)');
  });

  it('uses Boolean identities for empty operand lists', () => {
    expect(cel<ExampleResource>((filter) => filter.and())).toBeUndefined();
    expect(cel<ExampleResource>((filter) => filter.or())).toBe('false');
    expect(cel<ExampleResource>((filter) => filter.field('spec.tags').includesAny([]))).toBe(
      'false',
    );
  });

  it('ignores undefined operands in and/or expressions', () => {
    expect(
      cel<ExampleResource>((filter) =>
        filter.and(undefined, filter.field('id').equals('worker'), undefined),
      ),
    ).toBe('this.id == "worker"');
    expect(
      cel<ExampleResource>((filter) =>
        filter.or(undefined, filter.field('id').equals('worker'), undefined),
      ),
    ).toBe('(this.id == "worker")');
    expect(cel<ExampleResource>((filter) => filter.and(undefined, undefined))).toBeUndefined();
    expect(cel<ExampleResource>((filter) => filter.or(undefined, undefined))).toBe('false');
  });

  it('returns undefined for an empty top-level expression', () => {
    expect(cel<ExampleResource>(() => '')).toBeUndefined();
    expect(cel<ExampleResource>(() => undefined)).toBeUndefined();
    expect(cel<ExampleResource>((filter) => filter.group(filter.and()))).toBeUndefined();
  });

  it('restricts includes and includesAny to repeated fields', () => {
    cel<ExampleResource>((filter) => {
      // repeated field: includes/includesAny accept CelLiteral
      expectTypeOf(filter.field('spec.tags').includes)
        .parameter(0)
        .toEqualTypeOf<boolean | number | string>();
      expectTypeOf(filter.field('spec.tags').includesAny)
        .parameter(0)
        .toEqualTypeOf<readonly (boolean | number | string)[]>();

      // scalar fields: includes/includesAny resolve to never
      expectTypeOf(filter.field('spec.enabled').includes).parameter(0).toEqualTypeOf<never>();
      expectTypeOf(filter.field('spec.enabled').includesAny).parameter(0).toEqualTypeOf<never>();
      expectTypeOf(filter.field('metadata.name').includes).parameter(0).toEqualTypeOf<never>();

      return filter.field('id').equals('id');
    });
  });

  it('checks field values against the generated resource shape', () => {
    cel<ExampleResource>((filter) => {
      expectTypeOf(filter.field('metadata.name').equals).parameter(0).toEqualTypeOf<string>();
      expectTypeOf(filter.field('spec.replicas').equals).parameter(0).toEqualTypeOf<number>();
      return filter.field('id').equals('id');
    });
  });

  it('does not allow plain strings where a CEL filter is required', () => {
    expectTypeOf<string>().not.toMatchTypeOf<CelFilter<ExampleResource>>();
    expectTypeOf<CelFilter<ExampleResource>>().toMatchTypeOf<string>();
    expectTypeOf<{ filter: string }>().not.toMatchTypeOf<ListParams>();
  });
});
