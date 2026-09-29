import { type Client } from '@connectrpc/connect';

import { IdentityProviderPhase, IdentityProviders } from '@osac/types';

export const IDP_SYNC_POLL_MS = 500;
export const IDP_SYNC_POLL_MAX_ATTEMPTS = 20;

export const pollIdentityProviderUntilSynced = async (
  idpClient: Client<typeof IdentityProviders>,
  id: string,
) => {
  for (let attempt = 0; attempt < IDP_SYNC_POLL_MAX_ATTEMPTS; attempt++) {
    const resp = await idpClient.get({ id });
    if (!resp.object) {
      throw new Error('Identity provider not found in response');
    }
    const idp = resp.object;
    const phase = idp.status?.phase;
    if (phase === IdentityProviderPhase.READY) {
      return idp;
    }
    if (phase === IdentityProviderPhase.ERROR) {
      throw new Error(idp.status?.message || 'Identity provider sync failed');
    }
    await new Promise((resolve) => setTimeout(resolve, IDP_SYNC_POLL_MS));
  }
  throw new Error('Timed out waiting for the identity provider to sync');
};
