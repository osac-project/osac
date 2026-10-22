import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { BareMetalInstanceCatalogItemSchema } from '@osac/types';

import { getBareMetalInstanceInitialValues } from './fields';

describe('getBareMetalInstanceInitialValues', () => {
  it('loads a locked catalog disk image into the wizard', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        diskImage: {
          behavior: {
            case: 'locked',
            value: { id: 'disk-image-rhel', name: 'rhel' },
          },
        },
      },
    });

    expect(getBareMetalInstanceInitialValues(catalogItem).spec.diskImage).toEqual({
      value: { id: 'disk-image-rhel', name: 'rhel' },
      locked: true,
    });
  });

  it('loads an editable catalog disk image default into the wizard', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        diskImage: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: { id: 'disk-image-fedora', name: 'fedora' },
            },
          },
        },
      },
    });

    expect(getBareMetalInstanceInitialValues(catalogItem).spec.diskImage).toEqual({
      value: { id: 'disk-image-fedora', name: 'fedora' },
      locked: false,
    });
  });

  it('loads locked catalog network attachments into the wizard', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        networkAttachments: {
          behavior: {
            case: 'locked',
            value: {
              items: [
                {
                  subnet: { id: 'subnet-1', name: 'subnet-a' },
                  securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
                },
              ],
            },
          },
        },
      },
    });

    const { networkAttachment } = getBareMetalInstanceInitialValues(catalogItem).spec;

    expect(networkAttachment.locked).toBe(true);
    expect(networkAttachment.value.useDefaultNetwork).toBe(false);
    expect(networkAttachment.value.subnet).toEqual({
      id: 'subnet-1',
      name: 'subnet-a',
    });
    expect(networkAttachment.value.securityGroups).toEqual([{ id: 'sg-1', name: 'sg-web' }]);
  });

  it('loads locked auto external IP independently of network attachments', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        autoExternalIpAttachment: {
          behavior: {
            case: 'locked',
            value: true,
          },
        },
      },
    });

    const values = getBareMetalInstanceInitialValues(catalogItem);

    expect(values.spec.networkAttachment.locked).toBe(false);
    expect(values.spec.networkAttachment.value.useDefaultNetwork).toBe(true);
    expect(values.spec.autoExternalIpAttachment).toEqual({ value: true, locked: true });
  });

  it('loads an editable catalog network attachment default into the wizard', () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        networkAttachments: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: {
                items: [
                  {
                    subnet: { id: 'subnet-1', name: 'subnet-a' },
                    securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
                  },
                ],
              },
            },
          },
        },
      },
    });

    const { networkAttachment } = getBareMetalInstanceInitialValues(catalogItem).spec;

    expect(networkAttachment.locked).toBe(false);
    expect(networkAttachment.value.useDefaultNetwork).toBe(false);
    expect(networkAttachment.value.subnet).toEqual({
      id: 'subnet-1',
      name: 'subnet-a',
    });
    expect(networkAttachment.value.securityGroups).toEqual([{ id: 'sg-1', name: 'sg-web' }]);
  });
});
