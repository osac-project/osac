import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { IdentityProviderPhase } from '@osac/types';

import {
  IDP_SYNC_POLL_MAX_ATTEMPTS,
  IDP_SYNC_POLL_MS,
  pollIdentityProviderUntilSynced,
} from './identity-provider';

const makeIdp = (phase: IdentityProviderPhase, message = '') => ({
  id: 'idp-1',
  status: { phase, message },
});

const makeMockClient = (responses: ReturnType<typeof makeIdp>[]) => {
  let callIndex = 0;
  return {
    get: vi.fn(() => {
      const idp = responses[callIndex++];
      return Promise.resolve({ object: idp });
    }),
  } as unknown as Parameters<typeof pollIdentityProviderUntilSynced>[0];
};

describe('pollIdentityProviderUntilSynced', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns the IDP when phase is READY after one UNSPECIFIED poll', async () => {
    const readyIdp = makeIdp(IdentityProviderPhase.READY);
    const client = makeMockClient([makeIdp(IdentityProviderPhase.UNSPECIFIED), readyIdp]);

    const promise = pollIdentityProviderUntilSynced(client, 'idp-1');
    await vi.advanceTimersByTimeAsync(IDP_SYNC_POLL_MS);

    const result = await promise;
    expect(result).toEqual(readyIdp);
    expect(client.get).toHaveBeenCalledTimes(2);
  });

  it('throws when phase is ERROR with custom message', async () => {
    const client = makeMockClient([makeIdp(IdentityProviderPhase.ERROR, 'OIDC validation failed')]);

    const promise = pollIdentityProviderUntilSynced(client, 'idp-1');

    await expect(promise).rejects.toThrow('OIDC validation failed');
    expect(client.get).toHaveBeenCalledTimes(1);
  });

  it('throws when phase is ERROR with default message', async () => {
    const client = makeMockClient([makeIdp(IdentityProviderPhase.ERROR, '')]);

    const promise = pollIdentityProviderUntilSynced(client, 'idp-1');

    await expect(promise).rejects.toThrow('Identity provider sync failed');
    expect(client.get).toHaveBeenCalledTimes(1);
  });

  it('throws on timeout after max attempts of UNSPECIFIED', async () => {
    const responses = Array.from({ length: IDP_SYNC_POLL_MAX_ATTEMPTS }, () =>
      makeIdp(IdentityProviderPhase.UNSPECIFIED),
    );
    const client = makeMockClient(responses);

    const promise = pollIdentityProviderUntilSynced(client, 'idp-1');
    const caughtPromise = promise.catch((err: unknown) => err);

    for (let i = 0; i < IDP_SYNC_POLL_MAX_ATTEMPTS; i++) {
      await vi.advanceTimersByTimeAsync(IDP_SYNC_POLL_MS);
    }

    const error = await caughtPromise;
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toBe('Timed out waiting for the identity provider to sync');
    expect(client.get).toHaveBeenCalledTimes(IDP_SYNC_POLL_MAX_ATTEMPTS);
  });

  it('throws when response has no object', async () => {
    const client = {
      get: vi.fn(() => Promise.resolve({ object: undefined })),
    } as unknown as Parameters<typeof pollIdentityProviderUntilSynced>[0];

    const promise = pollIdentityProviderUntilSynced(client, 'idp-1');

    await expect(promise).rejects.toThrow('Identity provider not found in response');
    expect(client.get).toHaveBeenCalledTimes(1);
  });
});
