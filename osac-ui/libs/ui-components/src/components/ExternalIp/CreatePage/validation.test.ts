import { describe, expect, it } from 'vitest';

import { getExternalIpSchema } from './validation';
import { tIdentity as t } from '../../../test-utils/i18n';

describe('getExternalIpSchema', () => {
  it('allows an empty project the same way secrets do for Default', async () => {
    await expect(
      getExternalIpSchema(t).isValid({
        metadata: { name: 'edge-ip', project: '', description: '' },
        pool: { id: 'pool-1', name: 'public-edge' },
      }),
    ).resolves.toBe(true);
  });

  it('still requires a name and pool', async () => {
    await expect(
      getExternalIpSchema(t).isValid({
        metadata: { name: '', project: '', description: '' },
        pool: { id: '', name: '' },
      }),
    ).resolves.toBe(false);
  });
});
