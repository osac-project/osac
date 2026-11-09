import { Code, ConnectError, createClient, createRouterTransport } from '@connectrpc/connect';
import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { ComputeInstances } from '@osac/types';

import { getErrorMessage, parseHttpErrorBody } from './error';
import { createNetworkingImmutabilityInterceptor } from '../api/networking-immutability';

const tIdentity = ((key: string) => key) as unknown as TFunction;

describe('parseHttpErrorBody', () => {
  it('extracts message from gRPC-gateway JSON errors', () => {
    expect(
      parseHttpErrorBody(
        `{"code":3, "message":"field 'user_data' is required but no value was provided and no default is defined"}`,
      ),
    ).toBe("field 'user_data' is required but no value was provided and no default is defined");
  });

  it('returns plain text bodies unchanged', () => {
    expect(parseHttpErrorBody('upstream unavailable')).toBe('upstream unavailable');
  });
});

describe('getErrorMessage', () => {
  it('returns the middleware-decorated raw message once', async () => {
    const transport = createRouterTransport(
      (router) => {
        router.service(ComputeInstances, {
          update: () => {
            throw new ConnectError(
              'networkAttachments is immutable after creation',
              Code.InvalidArgument,
            );
          },
        });
      },
      {
        transport: {
          interceptors: [createNetworkingImmutabilityInterceptor(tIdentity)],
        },
      },
    );
    const client = createClient(ComputeInstances, transport);
    let caught: unknown;

    try {
      await client.update({
        object: { id: 'vm-1', spec: { instanceType: { id: 'standard-4-8' } } },
      });
    } catch (error) {
      caught = error;
    }

    const message = getErrorMessage(caught);
    expect(message).toBe(
      'networkAttachments is immutable after creation Delete and recreate the workload to change its network attachment.',
    );
    expect(
      message.match(/Delete and recreate the workload to change its network attachment\./g),
    ).toHaveLength(1);
  });

  it('leaves ordinary ConnectError raw messages unchanged', () => {
    expect(
      getErrorMessage(new ConnectError('cannot change GPU configuration', Code.FailedPrecondition)),
    ).toBe('cannot change GPU configuration');
  });
});
