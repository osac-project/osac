import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import { VolumeAccessMode } from '@osac/types';

import { getVolumeValidationSchema, volumeStepHasErrors } from './validation';
import type { VolumeFormValues } from './values';
import { tIdentity as t } from '../../test-utils/i18n';

const validValues: VolumeFormValues = {
  metadata: {
    name: 'data-volume',
    description: 'A block volume',
  },
  spec: {
    storageTier: { id: 'gold-block', name: 'gold-block' },
    sizeGib: '128',
    accessMode: VolumeAccessMode.READ_WRITE_ONCE,
  },
};

describe('getVolumeValidationSchema', () => {
  it('accepts a complete volume form', async () => {
    await expect(getVolumeValidationSchema(t).validate(validValues)).resolves.toMatchObject({
      metadata: validValues.metadata,
      spec: {
        ...validValues.spec,
        sizeGib: 128,
      },
    });
  });

  it.each([
    ['metadata.name', ''],
    ['metadata.name', 'Invalid_Name'],
    ['spec.storageTier.id', ''],
    ['spec.sizeGib', '0'],
    ['spec.sizeGib', 'not-a-number'],
    ['spec.accessMode', VolumeAccessMode.UNSPECIFIED],
  ])('rejects invalid value at %s', async (path, value) => {
    const values = structuredClone(validValues) as unknown as Record<string, unknown>;
    const [group, field, nestedField] = path.split('.');
    if (nestedField) {
      ((values[group] as Record<string, unknown>)[field] as Record<string, unknown>)[nestedField] =
        value;
    } else {
      (values[group] as Record<string, unknown>)[field] = value;
    }

    await expect(getVolumeValidationSchema(t).validate(values)).rejects.toBeInstanceOf(
      ValidationError,
    );
  });
});

describe('volumeStepHasErrors', () => {
  it.each([
    ['general', { metadata: { name: 'Name is required' } }],
    ['configuration', { spec: { storageTier: 'Storage tier is required' } }],
    ['configuration', { spec: { sizeGib: 'Must be greater than zero' } }],
    ['configuration', { spec: { accessMode: 'Access mode is required' } }],
  ])('reports errors for the %s step', (stepId, errors) => {
    expect(volumeStepHasErrors(stepId, errors)).toBe(true);
  });

  it('does not report review errors', () => {
    expect(volumeStepHasErrors('review', { metadata: { name: 'invalid' } })).toBe(false);
  });
});
