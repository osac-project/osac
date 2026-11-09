import { create } from '@bufbuild/protobuf';
import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';
import { describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstances,
  Clusters,
  ComputeInstances,
  ExternalIPAttachments,
  ExternalIPs,
  IdentityProviders,
  NATGateways,
  SecurityGroupSchema,
  SecurityGroups,
  Subnets,
  VirtualNetworks,
} from '@osac/types';

import {
  assertNetworkUpdateAllowed,
  createNetworkingImmutabilityInterceptor,
} from './networking-immutability';
import { tIdentity } from '../test-utils/i18n';

const PREFLIGHT = 'Network configuration cannot be changed after creation.';
const RESOURCE_GUIDANCE =
  'Delete and recreate the resource to change its networking. Remove dependent resources first. Tenant defaults cannot be deleted; create another resource instead.';
const WORKLOAD_GUIDANCE = 'Delete and recreate the workload to change its network attachment.';
const UNSUPPORTED_RULE_UPDATE = 'This server does not support updating security group rules.';

const UPDATELESS_NETWORKING_SERVICES = [
  VirtualNetworks,
  Subnets,
  ExternalIPs,
  ExternalIPAttachments,
  NATGateways,
] as const;

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

describe('assertNetworkUpdateAllowed', () => {
  describe('create/read/delete-only networking resources', () => {
    it.each(UPDATELESS_NETWORKING_SERVICES.map((service) => [service.typeName, service]))(
      'rejects every Update request for %s',
      (_serviceName, service) => {
        expect(() =>
          assertNetworkUpdateAllowed(service.typeName, { object: { id: 'resource-1' } }, tIdentity),
        ).toThrow(ConnectError);

        try {
          assertNetworkUpdateAllowed(service.typeName, { object: { id: 'resource-1' } }, tIdentity);
        } catch (error) {
          expect(error).toBeInstanceOf(ConnectError);
          expect((error as ConnectError).code).toBe(Code.FailedPrecondition);
          expect((error as ConnectError).rawMessage).toContain(PREFLIGHT);
          expect((error as ConnectError).rawMessage).toContain(RESOURCE_GUIDANCE);
        }
      },
    );
  });

  describe('workload network attachments', () => {
    it.each(
      WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
        fields.map((field) => [service.typeName, field] as const),
      ),
    )('rejects a %s mask that updates %s', (serviceTypeName, field) => {
      expect(() =>
        assertNetworkUpdateAllowed(
          serviceTypeName,
          { object: { id: 'workload-1' }, updateMask: { paths: [`spec.${field}`] } },
          tIdentity,
        ),
      ).toThrow(WORKLOAD_GUIDANCE);
    });

    it.each(
      WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
        fields.map((field) => [service.typeName, field] as const),
      ),
    )('rejects a %s mask that updates descendants of %s', (serviceTypeName, field) => {
      expect(() =>
        assertNetworkUpdateAllowed(
          serviceTypeName,
          {
            object: { id: 'workload-1' },
            updateMask: { paths: [`spec.${field}.subnet.id`] },
          },
          tIdentity,
        ),
      ).toThrow(WORKLOAD_GUIDANCE);
    });

    it.each(
      WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
        fields.map((field) => [service.typeName, camelCaseField(field)] as const),
      ),
    )('rejects a %s mask that updates descendants of camelCase %s', (serviceTypeName, field) => {
      expect(() =>
        assertNetworkUpdateAllowed(
          serviceTypeName,
          {
            object: { id: 'workload-1' },
            updateMask: { paths: [`spec.${field}.subnet.id`] },
          },
          tIdentity,
        ),
      ).toThrow(WORKLOAD_GUIDANCE);
    });

    it.each(
      WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
        fields.map((field) => [service.typeName, field] as const),
      ),
    )('rejects camelCase mask spellings of %s', (serviceTypeName, field) => {
      expect(() =>
        assertNetworkUpdateAllowed(
          serviceTypeName,
          {
            object: { id: 'workload-1' },
            updateMask: { paths: [`spec.${camelCaseField(field)}`] },
          },
          tIdentity,
        ),
      ).toThrow(WORKLOAD_GUIDANCE);
    });

    it.each(
      WORKLOAD_SERVICES.flatMap(({ service, fields }) =>
        fields.flatMap((field) =>
          [field, camelCaseField(field)].flatMap((spelling) =>
            [[], {}, null, true].map((value) => [service.typeName, spelling, value] as const),
          ),
        ),
      ),
    )(
      'rejects a supplied %s %s object property even without a mask',
      (serviceTypeName, spelling, value) => {
        expect(() =>
          assertNetworkUpdateAllowed(
            serviceTypeName,
            { object: { id: 'workload-1', spec: { [spelling]: value } } },
            tIdentity,
          ),
        ).toThrow(WORKLOAD_GUIDANCE);
      },
    );

    it.each(WORKLOAD_SERVICES.map(({ service }) => [service.typeName, service]))(
      'rejects a broad spec mask for %s',
      (serviceTypeName) => {
        expect(() =>
          assertNetworkUpdateAllowed(
            serviceTypeName,
            { object: { id: 'workload-1' }, updateMask: { paths: ['spec'] } },
            tIdentity,
          ),
        ).toThrow(WORKLOAD_GUIDANCE);
      },
    );

    it.each(WORKLOAD_SERVICES.map(({ service }) => [service.typeName, service]))(
      'does not reject a protected-field prefix for %s',
      (serviceTypeName) => {
        expect(() =>
          assertNetworkUpdateAllowed(
            serviceTypeName,
            {
              object: { id: 'workload-1' },
              updateMask: { paths: ['spec.network_attachment_note'] },
            },
            tIdentity,
          ),
        ).not.toThrow();
      },
    );

    it('allows an undefined protected property', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          ComputeInstances.typeName,
          { object: { id: 'workload-1', spec: { networkAttachments: undefined } } },
          tIdentity,
        ),
      ).not.toThrow();
    });

    it('allows narrow workload updates', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          ComputeInstances.typeName,
          {
            object: { id: 'vm-1', spec: { instanceType: { id: 'standard-4-8' } } },
            updateMask: { paths: ['spec.instance_type'] },
          },
          tIdentity,
        ),
      ).not.toThrow();

      expect(() =>
        assertNetworkUpdateAllowed(
          ComputeInstances.typeName,
          {
            object: { id: 'vm-1', spec: { runStrategy: 1, restartRequestedAt: {} } },
          },
          tIdentity,
        ),
      ).not.toThrow();

      expect(() =>
        assertNetworkUpdateAllowed(
          BareMetalInstances.typeName,
          { object: { id: 'bmi-1', spec: { runStrategy: 1 } } },
          tIdentity,
        ),
      ).not.toThrow();

      expect(() =>
        assertNetworkUpdateAllowed(
          ComputeInstances.typeName,
          { object: { id: 'vm-1', metadata: { name: 'renamed' } } },
          tIdentity,
        ),
      ).not.toThrow();
    });

    it('preserves unrelated service updates', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          IdentityProviders.typeName,
          { object: { id: 'idp-1', spec: { enabled: false } } },
          tIdentity,
        ),
      ).not.toThrow();
    });
  });

  describe('security group rule-only exception', () => {
    const rules = {
      ingress: [{ protocol: 6, portFrom: 443, portTo: 443 }],
      egress: [{ protocol: 6, portFrom: 443, portTo: 443 }],
    };

    it('allows complete ingress and egress replacement', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          {
            object: { id: 'sg-1', spec: rules },
            updateMask: { paths: ['spec.ingress', 'spec.egress'] },
          },
          tIdentity,
        ),
      ).not.toThrow();
    });

    it('allows rule-level mask paths', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          {
            object: { id: 'sg-1', spec: rules },
            updateMask: { paths: ['spec.ingress.protocol', 'spec.egress.ipv4_cidr'] },
          },
          tIdentity,
        ),
      ).not.toThrow();
    });

    it('allows explicitly clearing both lists', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          { object: { id: 'sg-1', spec: { ingress: [], egress: [] } } },
          tIdentity,
        ),
      ).not.toThrow();
    });

    it('allows a generated message spec with only rule lists', () => {
      const securityGroup = create(SecurityGroupSchema, {
        id: 'sg-1',
        spec: { ingress: [], egress: [] },
      });

      expect(() =>
        assertNetworkUpdateAllowed(SecurityGroups.typeName, { object: securityGroup }, tIdentity),
      ).not.toThrow();
    });

    it.each(['ingress', 'egress'])('rejects a missing %s list', (missing) => {
      const sparseRules: Record<string, unknown> = { ...rules };
      delete sparseRules[missing];

      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          { object: { id: 'sg-1', spec: sparseRules } },
          tIdentity,
        ),
      ).toThrow(RESOURCE_GUIDANCE);
    });

    it('rejects a missing spec or id', () => {
      expect(() =>
        assertNetworkUpdateAllowed(SecurityGroups.typeName, { object: { id: 'sg-1' } }, tIdentity),
      ).toThrow(PREFLIGHT);

      expect(() =>
        assertNetworkUpdateAllowed(SecurityGroups.typeName, { object: { spec: rules } }, tIdentity),
      ).toThrow(PREFLIGHT);
    });

    it('rejects a broad spec mask', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          {
            object: { id: 'sg-1', spec: rules },
            updateMask: { paths: ['spec'] },
          },
          tIdentity,
        ),
      ).toThrow(RESOURCE_GUIDANCE);
    });

    it.each(['id', 'metadata', 'metadata.name', 'status', 'spec.virtual_network'])(
      'rejects a %s mask',
      (path) => {
        expect(() =>
          assertNetworkUpdateAllowed(
            SecurityGroups.typeName,
            {
              object: { id: 'sg-1', spec: rules },
              updateMask: { paths: [path] },
            },
            tIdentity,
          ),
        ).toThrow(RESOURCE_GUIDANCE);
      },
    );

    it('rejects immutable object fields', () => {
      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          {
            object: {
              id: 'sg-1',
              metadata: { name: 'sg-web' },
              spec: { ...rules, virtualNetwork: { id: 'vn-1' } },
            },
          },
          tIdentity,
        ),
      ).toThrow(RESOURCE_GUIDANCE);

      expect(() =>
        assertNetworkUpdateAllowed(
          SecurityGroups.typeName,
          {
            object: { id: 'sg-1', spec: rules, status: { state: 1 } },
          },
          tIdentity,
        ),
      ).toThrow(RESOURCE_GUIDANCE);
    });
  });
});

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
