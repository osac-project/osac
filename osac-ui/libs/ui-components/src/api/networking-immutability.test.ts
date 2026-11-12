import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';
import { describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstances,
  Clusters,
  ComputeInstances,
  ExternalIPs,
  IdentityProviders,
  SecurityGroups,
  Subnets,
  VirtualNetworks,
} from '@osac/types';

import { createNetworkingImmutabilityInterceptor } from './networking-immutability';
import { tIdentity } from '../test-utils/i18n';

const RESOURCE_GUIDANCE =
  'Delete and recreate the resource to change its networking. Remove dependent resources first. Tenant defaults cannot be deleted; create another resource instead.';
const WORKLOAD_GUIDANCE = 'Delete and recreate the workload to change its network attachment.';
const UNSUPPORTED_RULE_UPDATE = 'This server does not support updating security group rules.';

const WORKLOAD_SERVICES = [
  {
    service: ComputeInstances,
    fields: ['network_attachments', 'auto_external_ip_attachment'],
  },
  {
    service: BareMetalInstances,
    fields: ['network_attachments', 'auto_external_ip_attachment'],
  },
  {
    service: Clusters,
    fields: ['network_attachment', 'auto_external_ip_attachment'],
  },
] as const;

const camelCaseField = (field: string): string =>
  field.replace(/_([a-z])/g, (_match, letter: string) => letter.toUpperCase());

type InterceptorRequest = Parameters<ReturnType<Interceptor>>[0];

const invokeInterceptor = async (
  serviceTypeName: string,
  methodName: string,
  error: Error,
): Promise<unknown> => {
  const interceptor = createNetworkingImmutabilityInterceptor(tIdentity);
  const next = vi.fn(() => Promise.reject(error));
  const handler = interceptor(next as unknown as Parameters<Interceptor>[0]);
  return handler({
    service: { typeName: serviceTypeName },
    method: { name: methodName },
  } as unknown as InterceptorRequest);
};

describe('createNetworkingImmutabilityInterceptor', () => {
  it('adds resource replacement guidance to unsupported networking updates', async () => {
    const original = new ConnectError('Update is not implemented', Code.Unimplemented);

    await expect(
      invokeInterceptor(VirtualNetworks.typeName, 'Update', original),
    ).rejects.toMatchObject({
      code: Code.Unimplemented,
      rawMessage: `${original.rawMessage} ${RESOURCE_GUIDANCE}`,
      cause: original,
    });
  });

  it('adds workload replacement guidance to immutable attachment rejections', async () => {
    const original = new ConnectError(
      'field network_attachments is immutable and cannot be changed',
      Code.InvalidArgument,
    );

    await expect(
      invokeInterceptor(ComputeInstances.typeName, ComputeInstances.method.update.name, original),
    ).rejects.toMatchObject({
      code: Code.InvalidArgument,
      rawMessage: `${original.rawMessage} ${WORKLOAD_GUIDANCE}`,
    });
  });

  it.each([
    'cannot change number of network attachments',
    'cannot change number of network attachments from 1 to 2',
  ])('adds workload guidance to the tokenless ComputeInstances count error %s', async (message) => {
    const original = new ConnectError(message, Code.InvalidArgument);

    await expect(
      invokeInterceptor(ComputeInstances.typeName, ComputeInstances.method.update.name, original),
    ).rejects.toMatchObject({
      code: Code.InvalidArgument,
      rawMessage: `${original.rawMessage} ${WORKLOAD_GUIDANCE}`,
    });
  });

  it('does not apply the tokenless count fallback to another workload service', async () => {
    const original = new ConnectError(
      'cannot change number of network attachments',
      Code.InvalidArgument,
    );

    try {
      await invokeInterceptor(
        BareMetalInstances.typeName,
        BareMetalInstances.method.update.name,
        original,
      );
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('does not apply the tokenless count fallback to a non-InvalidArgument error', async () => {
    const original = new ConnectError('cannot change number of network attachments', Code.Internal);

    try {
      await invokeInterceptor(
        ComputeInstances.typeName,
        ComputeInstances.method.update.name,
        original,
      );
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('does not match a near-miss tokenless count message', async () => {
    const original = new ConnectError('cannot change network attachments', Code.InvalidArgument);

    try {
      await invokeInterceptor(
        ComputeInstances.typeName,
        ComputeInstances.method.update.name,
        original,
      );
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('recognizes camelCase attachment names in workload errors', async () => {
    const original = new ConnectError(
      'networkAttachment cannot be changed after creation',
      Code.InvalidArgument,
    );

    await expect(
      invokeInterceptor(Clusters.typeName, Clusters.method.update.name, original),
    ).rejects.toMatchObject({ rawMessage: `${original.rawMessage} ${WORKLOAD_GUIDANCE}` });
  });

  it.each(
    WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
      fields.flatMap((field) => [
        [service.typeName, field] as const,
        [service.typeName, camelCaseField(field)] as const,
      ]),
    ),
  )('adds workload guidance for a %s error naming %s', async (serviceTypeName, spelling) => {
    const original = new ConnectError(`${spelling} is immutable`, Code.InvalidArgument);

    await expect(invokeInterceptor(serviceTypeName, 'Update', original)).rejects.toMatchObject({
      rawMessage: `${original.rawMessage} ${WORKLOAD_GUIDANCE}`,
    });
  });

  it.each(
    WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
      fields.flatMap((field) => [
        [service.typeName, `${field}_note`] as const,
        [service.typeName, `${camelCaseField(field)}Note`] as const,
      ]),
    ),
  )('does not match the lookalike field %s on %s', async (serviceTypeName, lookalike) => {
    const original = new ConnectError(`${lookalike} is immutable`, Code.InvalidArgument);

    try {
      await invokeInterceptor(serviceTypeName, 'Update', original);
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('does not match the Cluster plural attachment lookalike', async () => {
    const original = new ConnectError('network_attachments is immutable', Code.InvalidArgument);

    try {
      await invokeInterceptor(Clusters.typeName, Clusters.method.update.name, original);
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('qualifies security group rule-update guidance for servers without the operation', async () => {
    const original = new ConnectError('unknown method Update', Code.Unimplemented);

    await expect(
      invokeInterceptor(SecurityGroups.typeName, SecurityGroups.method.update.name, original),
    ).rejects.toMatchObject({
      rawMessage: `${original.rawMessage} ${UNSUPPORTED_RULE_UPDATE} ${RESOURCE_GUIDANCE}`,
    });
  });

  it('adds resource guidance to immutable networking spec errors', async () => {
    const original = new ConnectError('the spec is immutable after creation', Code.InvalidArgument);

    await expect(invokeInterceptor(Subnets.typeName, 'Update', original)).rejects.toMatchObject({
      rawMessage: `${original.rawMessage} ${RESOURCE_GUIDANCE}`,
    });
  });

  it('preserves code, metadata, details, and cause when decorating', async () => {
    const original = new ConnectError(
      'spec is immutable',
      Code.InvalidArgument,
      new Headers({ 'x-request-id': 'request-1' }),
    );
    original.details = [{ type: 'test.Detail', value: new Uint8Array([1, 2, 3]) }];

    try {
      await invokeInterceptor(ExternalIPs.typeName, 'Update', original);
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      const decorated = error as ConnectError;
      expect(decorated).not.toBe(original);
      expect(decorated.code).toBe(original.code);
      expect(decorated.metadata.get('x-request-id')).toBe('request-1');
      expect(decorated.details).toEqual(original.details);
      expect(decorated.cause).toBe(original);
    }
  });

  it.each([
    [
      'rule validation',
      Subnets.typeName,
      'Update',
      new ConnectError('invalid port 0', Code.InvalidArgument),
    ],
    [
      'readiness',
      SecurityGroups.typeName,
      SecurityGroups.method.update.name,
      new ConnectError('security group is provisioning', Code.FailedPrecondition),
    ],
    [
      'missing resource',
      VirtualNetworks.typeName,
      'Update',
      new ConnectError('resource not found', Code.NotFound),
    ],
    [
      'authentication',
      ComputeInstances.typeName,
      ComputeInstances.method.update.name,
      new ConnectError('session expired', Code.Unauthenticated),
    ],
    [
      'unrelated service',
      IdentityProviders.typeName,
      IdentityProviders.method.update.name,
      new ConnectError('spec is immutable', Code.InvalidArgument),
    ],
    [
      'non-update method',
      ComputeInstances.typeName,
      'Get',
      new ConnectError('field network_attachments is immutable', Code.InvalidArgument),
    ],
    [
      'permission denied',
      SecurityGroups.typeName,
      SecurityGroups.method.update.name,
      new ConnectError('permission denied', Code.PermissionDenied),
    ],
    [
      'connectivity',
      VirtualNetworks.typeName,
      'Update',
      new ConnectError('connection reset', Code.Unavailable),
    ],
    [
      'generic workload error',
      ComputeInstances.typeName,
      ComputeInstances.method.update.name,
      new ConnectError('template not found', Code.InvalidArgument),
    ],
  ])('returns %s errors unchanged', async (_label, serviceTypeName, methodName, original) => {
    try {
      await invokeInterceptor(serviceTypeName, methodName, original);
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('returns non-Connect errors unchanged', async () => {
    const original = new Error('network down');

    try {
      await invokeInterceptor(VirtualNetworks.typeName, 'Update', original);
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });

  it('does not duplicate existing replacement guidance', async () => {
    const original = new ConnectError(
      'spec is immutable; delete and recreate the resource to change its networking',
      Code.InvalidArgument,
    );

    try {
      await invokeInterceptor(Subnets.typeName, 'Update', original);
      expect.fail('expected the interceptor to reject');
    } catch (error) {
      expect(error).toBe(original);
    }
  });
});
