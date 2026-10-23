import { describe, expect, it } from 'vitest';

import { networkAttachmentFieldsFromPolicy } from './networkAttachments';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

describe('networkAttachmentFieldsFromPolicy', () => {
  it('treats locked catalog attachments as one policy unit on the default-network field', () => {
    expect(
      networkAttachmentFieldsFromPolicy({
        behavior: {
          case: 'locked',
          value: {
            subnet: { id: 'subnet-1', name: 'subnet-a' },
            virtualNetwork: { id: 'vn-1', name: 'vn-prod' },
            securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
          },
        },
      }),
    ).toEqual({
      value: {
        useDefaultNetwork: false,
        virtualNetwork: { id: 'vn-1', name: 'vn-prod' },
        subnet: { id: 'subnet-1', name: 'subnet-a' },
        securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
      },
      locked: true,
    });
  });

  it('seeds editable catalog attachments without locking the pickers', () => {
    expect(
      networkAttachmentFieldsFromPolicy({
        behavior: {
          case: 'editable',
          value: {
            defaultValue: {
              subnet: { id: 'subnet-1', name: 'subnet-a' },
              securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
            },
          },
        },
      }),
    ).toEqual({
      value: {
        useDefaultNetwork: false,
        virtualNetwork: emptyResourceSelectValue(),
        subnet: { id: 'subnet-1', name: 'subnet-a' },
        securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
      },
      locked: false,
    });
  });

  it('defaults to the tenant network when the catalog has no custom attachment', () => {
    expect(networkAttachmentFieldsFromPolicy({ behavior: { case: undefined } })).toEqual({
      value: {
        useDefaultNetwork: true,
        virtualNetwork: emptyResourceSelectValue(),
        subnet: emptyResourceSelectValue(),
        securityGroups: [],
      },
      locked: false,
    });
  });
});
