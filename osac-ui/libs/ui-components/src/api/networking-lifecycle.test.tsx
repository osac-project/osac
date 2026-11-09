import {
  type ConnectRouter,
  type Transport,
  createClient,
  createRouterTransport,
} from '@connectrpc/connect';
import { describe, expect, it } from 'vitest';

import type {
  ExternalIP,
  ExternalIPAttachment,
  ExternalIPAttachmentsCreateRequest,
  ExternalIPAttachmentsDeleteRequest,
  ExternalIPsCreateRequest,
  ExternalIPsDeleteRequest,
  NATGateway,
  NATGatewaysCreateRequest,
  NATGatewaysDeleteRequest,
  SecurityGroup,
  SecurityGroupsCreateRequest,
  SecurityGroupsDeleteRequest,
  SecurityGroupsUpdateRequest,
  Subnet,
  SubnetsCreateRequest,
  SubnetsDeleteRequest,
  VirtualNetwork,
  VirtualNetworksCreateRequest,
  VirtualNetworksDeleteRequest,
} from '@osac/types';
import {
  ExternalIPAttachments,
  ExternalIPState,
  ExternalIPs,
  NATGateways,
  Protocol,
  SecurityGroupState,
  SecurityGroups,
  SubnetState,
  Subnets,
  VirtualNetworkState,
  VirtualNetworks,
} from '@osac/types';
import { ExternalIPAttachmentEndpoint } from '@osac/types';

const virtualNetwork = {
  id: 'vn-1',
  metadata: { name: 'vn-prod' },
  spec: { ipv4Cidr: '10.0.0.0/16' },
  status: { state: VirtualNetworkState.READY },
} as VirtualNetwork;

const subnet = {
  id: 'subnet-1',
  metadata: { name: 'subnet-a' },
  spec: { virtualNetwork: { id: 'vn-1' }, ipv4Cidr: '10.0.1.0/24' },
  status: { state: SubnetState.READY },
} as Subnet;

const securityGroup = {
  id: 'sg-1',
  metadata: { name: 'sg-web' },
  spec: { virtualNetwork: { id: 'vn-1' }, ingress: [], egress: [] },
  status: { state: SecurityGroupState.READY },
} as unknown as SecurityGroup;

const externalIp = {
  id: 'eip-1',
  metadata: { name: 'eip-1' },
  spec: { pool: { id: 'pool-1' } },
  status: { state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, attached: false },
} as ExternalIP;

const externalIpAttachment = {
  id: 'attachment-1',
  metadata: { name: 'attachment-1' },
  spec: {
    externalIp: { id: 'eip-1' },
    target: { case: 'cluster', value: { id: 'cluster-1' } },
    targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
  },
} as ExternalIPAttachment;

const natGateway = {
  id: 'nat-1',
  metadata: { name: 'nat-egress' },
  spec: { virtualNetwork: { id: 'vn-1' }, externalIp: { id: 'eip-1' } },
} as NATGateway;

const createRecordingTransport = (routes: (router: ConnectRouter) => void) => {
  const inner = createRouterTransport(routes);
  const calls: string[] = [];
  const transport: Transport = {
    ...inner,
    unary(method, ...args) {
      calls.push(`${method.parent.typeName}.${method.name}`);
      return inner.unary(method, ...args);
    },
  };
  return { transport, calls };
};

describe('networking resource lifecycles', () => {
  it('keeps VirtualNetwork list, get, create, and delete dispatch', async () => {
    let createRequest: VirtualNetworksCreateRequest | undefined;
    let deleteRequest: VirtualNetworksDeleteRequest | undefined;
    const { transport, calls } = createRecordingTransport((router) => {
      router.service(VirtualNetworks, {
        list: () => ({ items: [virtualNetwork], size: 1, total: 1 }),
        get: (req) => ({ object: req.id === virtualNetwork.id ? virtualNetwork : undefined }),
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'vn-created' } };
        },
        delete: (req) => {
          deleteRequest = req;
          return {};
        },
      });
    });
    const client = createClient(VirtualNetworks, transport);

    expect((await client.list({})).items).toHaveLength(1);
    expect((await client.get({ id: virtualNetwork.id })).object?.id).toBe('vn-1');
    const created = await client.create({
      object: { metadata: { name: 'vn-new' }, spec: { ipv4Cidr: '10.1.0.0/16' } },
    });
    expect(created.object?.id).toBe('vn-created');
    await client.delete({ id: created.object?.id ?? '' });

    expect(createRequest?.object?.metadata?.name).toBe('vn-new');
    expect(createRequest?.object?.spec?.ipv4Cidr).toBe('10.1.0.0/16');
    expect(deleteRequest?.id).toBe('vn-created');
    expect(calls).toEqual([
      `${VirtualNetworks.typeName}.List`,
      `${VirtualNetworks.typeName}.Get`,
      `${VirtualNetworks.typeName}.Create`,
      `${VirtualNetworks.typeName}.Delete`,
    ]);
    expect(calls.some((call) => call.endsWith('.Update'))).toBe(false);
  });

  it('keeps Subnet list, get, create, and delete dispatch', async () => {
    let createRequest: SubnetsCreateRequest | undefined;
    let deleteRequest: SubnetsDeleteRequest | undefined;
    const { transport, calls } = createRecordingTransport((router) => {
      router.service(Subnets, {
        list: () => ({ items: [subnet], size: 1, total: 1 }),
        get: (req) => ({ object: req.id === subnet.id ? subnet : undefined }),
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'subnet-created' } };
        },
        delete: (req) => {
          deleteRequest = req;
          return {};
        },
      });
    });
    const client = createClient(Subnets, transport);

    expect((await client.list({})).items).toHaveLength(1);
    expect((await client.get({ id: subnet.id })).object?.id).toBe('subnet-1');
    const created = await client.create({
      object: {
        metadata: { name: 'subnet-new', project: 'project-a' },
        spec: { virtualNetwork: { name: 'vn-prod' }, ipv4Cidr: '10.0.2.0/24' },
      },
    });
    expect(created.object?.id).toBe('subnet-created');
    await client.delete({ id: created.object?.id ?? '' });

    expect(createRequest?.object?.metadata?.name).toBe('subnet-new');
    expect(createRequest?.object?.metadata?.project).toBe('project-a');
    expect(createRequest?.object?.spec?.virtualNetwork?.name).toBe('vn-prod');
    expect(createRequest?.object?.spec?.ipv4Cidr).toBe('10.0.2.0/24');
    expect(deleteRequest?.id).toBe('subnet-created');
    expect(calls).toEqual([
      `${Subnets.typeName}.List`,
      `${Subnets.typeName}.Get`,
      `${Subnets.typeName}.Create`,
      `${Subnets.typeName}.Delete`,
    ]);
    expect(calls.some((call) => call.endsWith('.Update'))).toBe(false);
  });

  it('keeps SecurityGroup lifecycle dispatch and the permitted rule Update', async () => {
    let createRequest: SecurityGroupsCreateRequest | undefined;
    let updateRequest: SecurityGroupsUpdateRequest | undefined;
    let deleteRequest: SecurityGroupsDeleteRequest | undefined;
    const { transport, calls } = createRecordingTransport((router) => {
      router.service(SecurityGroups, {
        list: () => ({ items: [securityGroup], size: 1, total: 1 }),
        get: (req) => ({ object: req.id === securityGroup.id ? securityGroup : undefined }),
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'sg-created' } };
        },
        update: (req) => {
          updateRequest = req;
          return { object: req.object };
        },
        delete: (req) => {
          deleteRequest = req;
          return {};
        },
      });
    });
    const client = createClient(SecurityGroups, transport);

    expect((await client.list({})).items).toHaveLength(1);
    expect((await client.get({ id: securityGroup.id })).object?.id).toBe('sg-1');
    const created = await client.create({
      object: {
        metadata: { name: 'sg-new', project: 'project-a' },
        spec: {
          virtualNetwork: { id: 'vn-1' },
          ingress: [{ protocol: Protocol.TCP, portFrom: 443, portTo: 443 }],
          egress: [],
        },
      },
    });
    expect(created.object?.id).toBe('sg-created');
    await client.update({
      object: {
        id: created.object?.id ?? '',
        spec: { ingress: [], egress: [{ protocol: Protocol.ALL }] },
      },
    });
    await client.delete({ id: created.object?.id ?? '' });

    expect(createRequest?.object?.metadata?.name).toBe('sg-new');
    expect(createRequest?.object?.spec?.virtualNetwork?.id).toBe('vn-1');
    expect(createRequest?.object?.spec?.ingress).toHaveLength(1);
    expect(updateRequest?.object?.id).toBe('sg-created');
    expect(updateRequest?.object?.spec?.ingress).toEqual([]);
    expect(updateRequest?.object?.spec?.egress).toHaveLength(1);
    expect(deleteRequest?.id).toBe('sg-created');
    expect(calls).toEqual([
      `${SecurityGroups.typeName}.List`,
      `${SecurityGroups.typeName}.Get`,
      `${SecurityGroups.typeName}.Create`,
      `${SecurityGroups.typeName}.Update`,
      `${SecurityGroups.typeName}.Delete`,
    ]);
  });

  it('keeps ExternalIP list, get, create, and delete dispatch', async () => {
    let createRequest: ExternalIPsCreateRequest | undefined;
    let deleteRequest: ExternalIPsDeleteRequest | undefined;
    const { transport, calls } = createRecordingTransport((router) => {
      router.service(ExternalIPs, {
        list: () => ({ items: [externalIp], size: 1, total: 1 }),
        get: (req) => ({ object: req.id === externalIp.id ? externalIp : undefined }),
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'eip-created' } };
        },
        delete: (req) => {
          deleteRequest = req;
          return {};
        },
      });
    });
    const client = createClient(ExternalIPs, transport);

    expect((await client.list({})).items).toHaveLength(1);
    expect((await client.get({ id: externalIp.id })).object?.id).toBe('eip-1');
    const created = await client.create({
      object: { metadata: { name: 'eip-new' }, spec: { pool: { id: 'pool-1' } } },
    });
    expect(created.object?.id).toBe('eip-created');
    await client.delete({ id: created.object?.id ?? '' });

    expect(createRequest?.object?.metadata?.name).toBe('eip-new');
    expect(createRequest?.object?.spec?.pool?.id).toBe('pool-1');
    expect(deleteRequest?.id).toBe('eip-created');
    expect(calls).toEqual([
      `${ExternalIPs.typeName}.List`,
      `${ExternalIPs.typeName}.Get`,
      `${ExternalIPs.typeName}.Create`,
      `${ExternalIPs.typeName}.Delete`,
    ]);
    expect(calls.some((call) => call.endsWith('.Update'))).toBe(false);
  });

  it('keeps ExternalIPAttachment list, get, create, and delete dispatch', async () => {
    let createRequest: ExternalIPAttachmentsCreateRequest | undefined;
    let deleteRequest: ExternalIPAttachmentsDeleteRequest | undefined;
    const { transport, calls } = createRecordingTransport((router) => {
      router.service(ExternalIPAttachments, {
        list: () => ({ items: [externalIpAttachment], size: 1, total: 1 }),
        get: (req) => ({
          object: req.id === externalIpAttachment.id ? externalIpAttachment : undefined,
        }),
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'attachment-created' } };
        },
        delete: (req) => {
          deleteRequest = req;
          return {};
        },
      });
    });
    const client = createClient(ExternalIPAttachments, transport);

    expect((await client.list({})).items).toHaveLength(1);
    expect((await client.get({ id: externalIpAttachment.id })).object?.id).toBe('attachment-1');
    const created = await client.create({
      object: {
        metadata: { name: 'attachment-new' },
        spec: {
          externalIp: { id: 'eip-1' },
          target: { case: 'cluster', value: { id: 'cluster-1' } },
          targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
        },
      },
    });
    expect(created.object?.id).toBe('attachment-created');
    await client.delete({ id: created.object?.id ?? '' });

    expect(createRequest?.object?.metadata?.name).toBe('attachment-new');
    expect(createRequest?.object?.spec?.externalIp?.id).toBe('eip-1');
    expect(createRequest?.object?.spec?.target.case).toBe('cluster');
    expect(deleteRequest?.id).toBe('attachment-created');
    expect(calls).toEqual([
      `${ExternalIPAttachments.typeName}.List`,
      `${ExternalIPAttachments.typeName}.Get`,
      `${ExternalIPAttachments.typeName}.Create`,
      `${ExternalIPAttachments.typeName}.Delete`,
    ]);
    expect(calls.some((call) => call.endsWith('.Update'))).toBe(false);
  });

  it('keeps NATGateway list, get, create, and delete dispatch', async () => {
    let createRequest: NATGatewaysCreateRequest | undefined;
    let deleteRequest: NATGatewaysDeleteRequest | undefined;
    const { transport, calls } = createRecordingTransport((router) => {
      router.service(NATGateways, {
        list: () => ({ items: [natGateway], size: 1, total: 1 }),
        get: (req) => ({ object: req.id === natGateway.id ? natGateway : undefined }),
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'nat-created' } };
        },
        delete: (req) => {
          deleteRequest = req;
          return {};
        },
      });
    });
    const client = createClient(NATGateways, transport);

    expect((await client.list({})).items).toHaveLength(1);
    expect((await client.get({ id: natGateway.id })).object?.id).toBe('nat-1');
    const created = await client.create({
      object: {
        metadata: { name: 'nat-new' },
        spec: { virtualNetwork: { id: 'vn-1' }, externalIp: { id: 'eip-1' } },
      },
    });
    expect(created.object?.id).toBe('nat-created');
    await client.delete({ id: created.object?.id ?? '' });

    expect(createRequest?.object?.metadata?.name).toBe('nat-new');
    expect(createRequest?.object?.spec?.virtualNetwork?.id).toBe('vn-1');
    expect(createRequest?.object?.spec?.externalIp?.id).toBe('eip-1');
    expect(deleteRequest?.id).toBe('nat-created');
    expect(calls).toEqual([
      `${NATGateways.typeName}.List`,
      `${NATGateways.typeName}.Get`,
      `${NATGateways.typeName}.Create`,
      `${NATGateways.typeName}.Delete`,
    ]);
    expect(calls.some((call) => call.endsWith('.Update'))).toBe(false);
  });
});
