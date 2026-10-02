import { describe, expect, it } from 'vitest';

import { BareMetalInstanceRunStrategy } from '@osac/types';

import { createEmptyBareMetalInstanceValues } from './fields';
import { buildBareMetalInstanceCreatePayload } from './payload';

const buildValues = (project: string) => ({
  ...createEmptyBareMetalInstanceValues(),
  catalogItemId: 'catalog-bm-1',
  metadata: { name: 'my-bmi', project },
});

describe('buildBareMetalInstanceCreatePayload', () => {
  it('builds a catalog-item create payload', () => {
    expect(buildBareMetalInstanceCreatePayload(buildValues(''))).toEqual({
      metadata: { name: 'my-bmi', project: '' },
      spec: {
        catalogItem: { id: 'catalog-bm-1' },
        runStrategy: BareMetalInstanceRunStrategy.ALWAYS,
        diskImage: {
          id: '',
        },
      },
    });
  });

  it('includes the selected instance type', () => {
    const values = buildValues('');
    values.spec.instanceType.name = 'bare-metal.large';

    expect(buildBareMetalInstanceCreatePayload(values).spec?.instanceType).toEqual({
      name: 'bare-metal.large',
    });
  });

  it.each([
    ['default (no project)', ''],
    ['top-level project', 'my-project'],
    ['nested project path', 'parent.child'],
  ])('passes the selected %s through to metadata.project', (_label, project) => {
    expect(buildBareMetalInstanceCreatePayload(buildValues(project)).metadata).toEqual({
      name: 'my-bmi',
      project,
    });
  });

  it('includes the selected disk image reference', () => {
    const values = buildValues('');
    values.spec.diskImage = { id: 'disk-image-1', name: 'rhel' };

    expect(buildBareMetalInstanceCreatePayload(values).spec?.diskImage).toEqual({
      id: 'disk-image-1',
    });
  });

  it('sends inline user data when the inline source is selected', () => {
    const values = buildValues('');
    values.spec.userDataSource = 'inline';
    values.spec.userData = '  #cloud-config\nusers: []  ';
    values.spec.userDataSecret = { name: 'stale-secret' };

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec?.userData).toBe('#cloud-config\nusers: []');
    expect(payload.spec).not.toHaveProperty('userDataSecret');
  });

  it('sends a Secret reference when the Secret source is selected', () => {
    const values = buildValues('');
    values.spec.userDataSource = 'secret';
    values.spec.userData = 'stale inline data';
    values.spec.userDataSecret = { name: '  cloud-init  ' };

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec?.userDataSecret).toEqual({ name: 'cloud-init' });
    expect(payload.spec).not.toHaveProperty('userData');
  });

  it('omits user data when the selected source is empty', () => {
    const values = buildValues('');
    values.spec.userDataSource = 'secret';
    values.spec.userData = 'stale inline data';
    values.spec.userDataSecret = { name: '   ' };

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec).not.toHaveProperty('userData');
    expect(payload.spec).not.toHaveProperty('userDataSecret');
  });
});
