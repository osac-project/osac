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
  it('gets one subnet parent and uses direct reference links without preflight SSAR', async () => {
    const transport = createMockConnectTransport(
      {
        subnets: [
          create(SubnetSchema, {
            id: 'subnet-1',
            metadata: { name: 'Do not substitute fetched name' },
            spec: { virtualNetwork: { id: 'vnet/1', name: 'Tenant network' } },
          }),
        ],
      },
      {
        onSelfSubjectAccessReview: () => ({ object: { status: { allowed: false } } }),
      },
    );
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(<ClusterNetworkingCard cluster={cluster} />, { transport });
    expect(await screen.findByRole('link', { name: 'Tenant network' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vnet%2F1',
    );
    expect(screen.getByRole('link', { name: 'Alpha' })).toHaveAttribute(
      'href',
      '/networking/security-groups/sg%2F1',
    );
    expect(screen.getByRole('link', { name: 'Beta' })).toHaveAttribute(
      'href',
      '/networking/security-groups/sg-2',
    );
    expect(screen.getByText(/Name only/).closest('a')).toBeNull();
    expect(screen.getByText('tenant-subnet').closest('a')).toBeNull();
    expect(screen.queryByText('Do not substitute fetched name')).not.toBeInTheDocument();
    const table = screen.getByRole('grid', { name: 'Cluster network attachment' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((header) => header.textContent),
    ).toEqual(['Virtual network', 'Security groups', 'Subnet']);
    expect(table).toHaveClass('pf-m-compact', 'pf-m-plain');
    expect(within(table).queryByRole('button')).not.toBeInTheDocument();
    expect(within(table).queryByRole('combobox')).not.toBeInTheDocument();
    expect(unary.mock.calls.map(([method]) => method)).toEqual([Subnets.method.get]);
    expect(unary.mock.calls[0][4]).toEqual({ id: 'subnet-1' });
  });

  it.each(['missing', 'failure', 'no-parent', 'name-only-parent'])(
    'keeps attachment references when subnet lookup has %s',
    async (scenario) => {
      const transport = createMockConnectTransport(
        {},
        {
          onSubnetGet: () => {
            if (scenario === 'failure') {
              throw new Error('Subnet unavailable');
            }
            if (scenario === 'missing') {
              return {};
            }
            return {
              object: create(SubnetSchema, {
                id: 'subnet-1',
                spec:
                  scenario === 'name-only-parent'
                    ? { virtualNetwork: { name: 'Name only network' } }
                    : {},
              }),
            };
          },
        },
      );
      const unary = vi.spyOn(transport, 'unary');
      renderWithProviders(<ClusterNetworkingCard cluster={cluster} />, { transport });
      await waitFor(() => expect(unary).toHaveBeenCalledTimes(1));
      expect(screen.getByText('tenant-subnet')).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'Alpha' })).toBeInTheDocument();
      expect(screen.queryByRole('link', { name: 'tenant-subnet' })).not.toBeInTheDocument();
      if (scenario === 'name-only-parent') {
        expect((await screen.findByText('Name only network')).closest('a')).toBeNull();
      } else {
        expect(screen.getByRole('cell', { name: '—' })).toBeInTheDocument();
      }
    },
  );

  it('does not reuse the previous subnet parent after the reference changes', async () => {
    const transport = createMockConnectTransport({
      subnets: [
        create(SubnetSchema, {
          id: 'subnet-1',
          spec: { virtualNetwork: { id: 'old-network', name: 'Old network' } },
        }),
      ],
    });
    const unary = vi.spyOn(transport, 'unary');
    const { rerender } = renderWithProviders(<ClusterNetworkingCard cluster={cluster} />, {
      transport,
    });
    await screen.findByRole('link', { name: 'Old network' });
    rerender(
      <ClusterNetworkingCard
        cluster={create(ClusterSchema, {
          spec: { networkAttachment: { subnet: { id: 'subnet-2', name: 'New subnet' } } },
        })}
      />,
    );
    expect(screen.queryByRole('link', { name: 'Old network' })).not.toBeInTheDocument();
    expect(screen.getByText('New subnet')).toBeInTheDocument();
    await waitFor(() => expect(unary).toHaveBeenCalledTimes(2));
  });

  it('does not fetch a missing reference and shows placeholders without fabricated links', () => {
    const transport = createMockConnectTransport();
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(<ClusterNetworkingCard cluster={create(ClusterSchema)} />, { transport });
    expect(screen.getAllByText('—')).toHaveLength(3);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(unary).not.toHaveBeenCalled();
  });
});
