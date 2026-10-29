import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { BareMetalInstanceCatalogItemSchema, BareMetalInstanceRunStrategy } from '@osac/types';

import { createEmptyBareMetalInstanceValues } from './fields';
import { buildBareMetalInstanceCreatePayload } from './payload';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const buildValues = (project: string) => ({
  ...createEmptyBareMetalInstanceValues(),
  catalogItemReference: { name: 'catalog-bm-1', id: 'catalog-bm-1' },
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
        networkAttachments: [],
        autoExternalIpAttachment: false,
      },
    });
  });

  it('includes the selected instance type', () => {
    const values = buildValues('');
    values.spec.instanceType.value = 'bare-metal.large';

    expect(buildBareMetalInstanceCreatePayload(values).spec?.instanceType).toEqual({
      name: 'bare-metal.large',
      shared: true,
    });
  });

  it('omits a locked instance type so the catalog policy can apply it', () => {
    const values = buildValues('');
    values.spec.instanceType = provisioningFieldValue('bare-metal.large', true);

    expect(buildBareMetalInstanceCreatePayload(values).spec).not.toHaveProperty('instanceType');
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
    values.spec.diskImage.value = { id: 'disk-image-1', name: 'rhel' };

    expect(buildBareMetalInstanceCreatePayload(values).spec?.diskImage).toEqual({
      id: 'disk-image-1',
    });
  });

  it('omits a locked disk image so the catalog policy can apply it', () => {
    const values = buildValues('');
    values.spec.diskImage = provisioningFieldValue({ id: 'disk-image-1', name: 'rhel' }, true);

    expect(buildBareMetalInstanceCreatePayload(values).spec).not.toHaveProperty('diskImage');
  });

  it('omits run strategy when the catalog item defines it', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        runStrategy: {
          behavior: {
            case: 'locked',
            value: BareMetalInstanceRunStrategy.ALWAYS,
          },
        },
      },
    });

    expect(
      buildBareMetalInstanceCreatePayload(buildValues(''), catalogItem).spec,
    ).not.toHaveProperty('runStrategy');
  });

  it('sends run_strategy ALWAYS when the catalog policy makes it editable', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        runStrategy: {
          behavior: {
            case: 'editable',
            value: { defaultValue: BareMetalInstanceRunStrategy.HALTED },
          },
        },
      },
    });

    expect(
      buildBareMetalInstanceCreatePayload(buildValues(''), catalogItem).spec?.runStrategy,
    ).toBe(BareMetalInstanceRunStrategy.ALWAYS);
  });

  it('sends inline user data when the inline source is selected', () => {
    const values = buildValues('');
    values.spec.userDataSource = provisioningFieldValue('inline');
    values.spec.userData = provisioningFieldValue('  #cloud-config\nusers: []  ');
    values.spec.userDataSecret = provisioningFieldValue('stale-secret');

    const payload = buildBareMetalInstanceCreatePayload(values);

    //expect(payload.spec?.userData).toBe('#cloud-config\nusers: []');
    expect(payload.spec).not.toHaveProperty('userDataSecret');
  });

  it('sends a Secret reference when the Secret source is selected', () => {
    const values = buildValues('');
    values.spec.userDataSource = provisioningFieldValue('secret');
    values.spec.userData = provisioningFieldValue('stale inline data');
    values.spec.userDataSecret = provisioningFieldValue('  cloud-init  ');

    const payload = buildBareMetalInstanceCreatePayload(values);

    //expect(payload.spec?.userDataSecret).toEqual({ name: 'cloud-init' });
    expect(payload.spec).not.toHaveProperty('userData');
  });

  it('omits user data when the selected source is empty', () => {
    const values = buildValues('');
    values.spec.userDataSource = provisioningFieldValue('secret');
    values.spec.userData = provisioningFieldValue('stale inline data');
    values.spec.userDataSecret = provisioningFieldValue('   ');

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec).not.toHaveProperty('userData');
    expect(payload.spec).not.toHaveProperty('userDataSecret');
  });
});

describe('buildBareMetalInstanceCreatePayload — useDefaultNetwork', () => {
  it('sends an empty networkAttachments list when useDefaultNetwork is true', () => {
    const values = buildValues('');
    values.spec.networkAttachment.value.useDefaultNetwork = true;

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec?.networkAttachments).toEqual([]);
  });

  it('includes networkAttachments when useDefaultNetwork is false', () => {
    const values = buildValues('');
    values.spec.networkAttachment.value.useDefaultNetwork = false;
    values.spec.networkAttachment.value.subnet = {
      id: 'subnet-1',
      name: 'subnet-1',
    };
    values.spec.networkAttachment.value.securityGroups = [{ id: 'sg-1', name: 'sg-1' }];

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec?.networkAttachments).toHaveLength(1);
    expect(payload.spec?.networkAttachments?.[0]).toMatchObject({
      subnet: { id: 'subnet-1' },
      securityGroups: [{ id: 'sg-1' }],
    });
  });
});

describe('buildBareMetalInstanceCreatePayload — autoExternalIpAttachment', () => {
  it('includes autoExternalIpAttachment when disabled so catalog defaults are not applied', () => {
    const values = buildValues('');
    values.spec.autoExternalIpAttachment = provisioningFieldValue(false);

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec?.autoExternalIpAttachment).toBe(false);
  });

  it('sets autoExternalIpAttachment when enabled', () => {
    const values = buildValues('');
    values.spec.autoExternalIpAttachment = provisioningFieldValue(true);

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec?.autoExternalIpAttachment).toBe(true);
  });

  it('omits autoExternalIpAttachment when locked', () => {
    const values = buildValues('');
    values.spec.autoExternalIpAttachment = provisioningFieldValue(true, true);

    const payload = buildBareMetalInstanceCreatePayload(values);

    expect(payload.spec).not.toHaveProperty('autoExternalIpAttachment');
  });
});
