import type { ReactNode } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ExternalIP, ExternalIPAttachment, ExternalIPPool, NATGateway } from '@osac/types';
import {
  ExternalIPAttachmentSchema,
  ExternalIPPoolSchema,
  ExternalIPSchema,
  ExternalIPState,
  IPFamily,
  NATGatewaySchema,
} from '@osac/types';

import {
  attachmentExternalIpIdsFilter,
  buildAttachedTargetsByExternalIpId,
  computeInstanceAttachmentFilter,
  natGatewayExternalIpIdsFilter,
  poolIdsFilter,
  useExternalIpsData,
} from './external-ip-data';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';

const makePool = (id: string, name: string): ExternalIPPool =>
  create(ExternalIPPoolSchema, {
    id,
    metadata: { name },
    spec: { ipFamily: IPFamily.IP_FAMILY_IPV4 },
    status: { available: 4n },
  });

const makeExternalIp = (id: string, poolId: string, attached = false): ExternalIP =>
  create(ExternalIPSchema, {
    id,
    metadata: { name: id },
    spec: { pool: { id: poolId } },
    status: {
      state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
      address: '203.0.113.10',
      pool: poolId,
      attached,
    },
  });

const makeAttachment = (externalIpId: string, vmId: string, vmName: string): ExternalIPAttachment =>
  create(ExternalIPAttachmentSchema, {
    id: `att-${externalIpId}`,
    spec: {
      externalIp: { id: externalIpId },
      target: { case: 'computeInstance', value: { id: vmId, name: vmName } },
    },
  });

const makeNatGateway = (id: string, externalIpId: string, virtualNetworkId: string): NATGateway =>
  create(NATGatewaySchema, {
    id,
    metadata: { name: 'edge-nat' },
    spec: {
      virtualNetwork: { id: virtualNetworkId, name: 'edge-net' },
      externalIp: { id: externalIpId },
    },
  });

describe('external IP list join helpers', () => {
  it('filters pools, attachments, and NAT gateways by the displayed ids', () => {
    expect(poolIdsFilter(['pool-2', 'pool-1'])).toBe('this.id in ["pool-2", "pool-1"]');
    expect(attachmentExternalIpIdsFilter(['eip-2', 'eip-1'])).toBe(
      'this.spec.external_ip.id in ["eip-2", "eip-1"]',
    );
    expect(natGatewayExternalIpIdsFilter(['eip-2', 'eip-1'])).toBe(
      'this.spec.external_ip.id in ["eip-2", "eip-1"]',
    );
  });

  it('filters VM attachments by the compute instance', () => {
    expect(computeInstanceAttachmentFilter('vm-1')).toBe('this.spec.compute_instance.id == "vm-1"');
  });

  it('maps an attachment to the target resource link', () => {
    expect(buildAttachedTargetsByExternalIpId([makeAttachment('eip-1', 'vm-1', 'web-1')])).toEqual({
      'eip-1': {
        kind: 'computeInstance',
        name: 'web-1',
        href: '/vms/vm-1',
      },
    });
  });

  it('maps a NAT gateway to the virtual network details link', () => {
    expect(
      buildAttachedTargetsByExternalIpId([], [makeNatGateway('nat-1', 'eip-2', 'vn-1')]),
    ).toEqual({
      'eip-2': {
        kind: 'natGateway',
        name: 'edge-nat',
        href: '/networking/virtual-networks/vn-1',
      },
    });
  });
});

describe('useExternalIpsData', () => {
  it('loads pools and attachments for the currently displayed external IPs', async () => {
    const transport = createMockConnectTransport({
      externalIps: [makeExternalIp('eip-1', 'pool-1', true), makeExternalIp('eip-2', 'pool-1')],
      externalIpPools: [makePool('pool-1', 'public-edge'), makePool('pool-unused', 'other')],
      externalIpAttachments: [makeAttachment('eip-1', 'vm-1', 'web-1')],
    });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiProvider transport={transport}>
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      </ApiProvider>
    );

    const { result } = renderHook(() => useExternalIpsData(), { wrapper });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.externalIps.map((externalIp) => externalIp.id)).toEqual([
      'eip-1',
      'eip-2',
    ]);
    expect(result.current.poolsById['pool-1']?.metadata?.name).toBe('public-edge');
    expect(result.current.poolsById['pool-unused']).toBeUndefined();
    expect(result.current.attachedTargetsByExternalIpId['eip-1']?.href).toBe('/vms/vm-1');
    expect(result.current.attachedTargetsByExternalIpId['eip-2']).toBeUndefined();
  });

  it('loads NAT gateways for the currently displayed external IPs', async () => {
    const transport = createMockConnectTransport({
      externalIps: [makeExternalIp('eip-2', 'pool-1', true)],
      externalIpPools: [makePool('pool-1', 'public-edge')],
      natGateways: [makeNatGateway('nat-1', 'eip-2', 'vn-1')],
    });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <ApiProvider transport={transport}>
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      </ApiProvider>
    );

    const { result } = renderHook(() => useExternalIpsData(), { wrapper });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.attachedTargetsByExternalIpId['eip-2']).toEqual({
      kind: 'natGateway',
      name: 'edge-nat',
      href: '/networking/virtual-networks/vn-1',
    });
  });
});
