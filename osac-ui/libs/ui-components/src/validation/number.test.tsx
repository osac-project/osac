import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { INT32_MAX, INT64_MAX, bigIntSchema, intSchema } from './number';

const t = ((key: string) => key) as TFunction;

describe('bigIntSchema', () => {
  it('accepts values up to the signed int64 maximum', async () => {
    await expect(bigIntSchema(t).validate(INT64_MAX)).resolves.toBe(INT64_MAX);
  });

  it('rejects values above the signed int64 maximum', async () => {
    await expect(bigIntSchema(t).validate(INT64_MAX + 1n)).rejects.toMatchObject({
      message: 'Must be at most {{max}}',
    });
  });
});

describe('intSchema', () => {
  it('uses the whole-number message for non-numeric values', async () => {
    await expect(intSchema(t).validate(Number.NaN)).rejects.toMatchObject({
      message: 'Must be a whole number',
    });
  });

  it('enforces the signed int32 maximum by default', async () => {
    await expect(intSchema(t).validate(INT32_MAX)).resolves.toBe(INT32_MAX);
    await expect(intSchema(t).validate(INT32_MAX + 1)).rejects.toMatchObject({
      message: 'Must be at most {{max}}',
    });
  });
});
