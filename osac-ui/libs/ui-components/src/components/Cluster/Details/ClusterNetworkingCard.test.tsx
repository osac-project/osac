import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ClusterSchema, SubnetSchema, Subnets } from '@osac/types';

import ClusterNetworkingCard from './ClusterNetworkingCard';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const cluster = create(ClusterSchema, {
  id: 'cluster-1',
  spec: {
    networkAttachment: {
      subnet: { id: 'subnet-1', name: 'tenant-subnet' },
      securityGroups: [
        { id: 'sg/1', name: 'Alpha' },
        { id: 'sg-2', name: 'Beta' },
        { name: 'Name only' },
      ],
    },
  },
});

describe('ClusterNetworkingCard', () => {
  it('resolves the real subnet parent and preserves multiple security groups in user column order', async () => {
    const transport = createMockConnectTransport({
      subnets: [
        create(SubnetSchema, {
          id: 'subnet-1',
          spec: { virtualNetwork: { id: 'vnet/1', name: 'Tenant network' } },
        }),
      ],
    });
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(<ClusterNetworkingCard cluster={cluster} />, { transport });
    expect(await screen.findByRole('link', { name: 'Tenant network' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vnet%2F1',
    );
    expect(await screen.findByRole('link', { name: 'Alpha' })).toHaveAttribute(
      'href',
      '/networking/security-groups/sg%2F1',
    );
    expect(screen.getByRole('link', { name: 'Beta' })).toHaveAttribute(
      'href',
      '/networking/security-groups/sg-2',
    );
    expect(screen.getByText(/Name only/).closest('a')).toBeNull();
    expect(screen.getByText('tenant-subnet').closest('a')).toBeNull();
    const table = screen.getByRole('grid', { name: 'Cluster network attachment' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((header) => header.textContent),
    ).toEqual(['Virtual network', 'Security groups', 'Subnet']);
    expect(table).toHaveClass('pf-m-compact');
    expect(unary.mock.calls.filter(([method]) => method === Subnets.method.get)).toHaveLength(1);
  });

  it.each(['missing', 'failure', 'denied', 'no-parent', 'wrong-id'])(
    'retains subnet and group fields when subnet lookup is %s',
    async (scenario) => {
      const transport = createMockConnectTransport(
        {},
        {
          onSubnetGet: () => {
            if (scenario === 'failure') {
              throw new Error('Subnet unavailable');
            }
            if (scenario === 'wrong-id') {
              return {
                object: create(SubnetSchema, {
                  id: 'another-subnet',
                  spec: { virtualNetwork: { id: 'wrong-network', name: 'Wrong network' } },
                }),
              };
            }
            return scenario === 'no-parent'
              ? { object: create(SubnetSchema, { id: 'subnet-1' }) }
              : {};
          },
          onSelfSubjectAccessReview: () => ({
            object: { status: { allowed: scenario !== 'denied' } },
          }),
        },
      );
      const unary = vi.spyOn(transport, 'unary');
      renderWithProviders(<ClusterNetworkingCard cluster={cluster} />, { transport });
      await waitFor(() => expect(unary).toHaveBeenCalled());
      if (scenario !== 'denied') {
        await waitFor(() =>
          expect(unary.mock.calls.some(([method]) => method === Subnets.method.get)).toBe(true),
        );
      }
      expect(screen.getByText('tenant-subnet')).toBeInTheDocument();
      expect(screen.getByText('Alpha')).toBeInTheDocument();
      expect(screen.getByText(/Beta/)).toBeInTheDocument();
      expect(screen.getByRole('cell', { name: '—' })).toBeInTheDocument();
      expect(screen.queryByRole('link', { name: 'tenant-subnet' })).not.toBeInTheDocument();
    },
  );

  it('does not fetch a missing subnet reference and shows empty fallbacks', async () => {
    const transport = createMockConnectTransport();
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(<ClusterNetworkingCard cluster={create(ClusterSchema)} />, { transport });
    await waitFor(() => expect(unary).toHaveBeenCalled());
    expect(screen.getAllByText('—')).toHaveLength(3);
    expect(unary.mock.calls.some(([method]) => method === Subnets.method.get)).toBe(false);
  });
});
