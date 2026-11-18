import { Route, Routes, useParams } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { Subnet, SubnetsDeleteRequest, VirtualNetwork } from '@osac/types';
import { SubnetState, SubnetsDeleteResponseSchema, VirtualNetworkState } from '@osac/types';

import { SubnetDetailPage } from './SubnetDetailPage';
import type {
  MockApiFixtures,
  MockTransportOverrides,
} from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockVirtualNetwork = {
  id: 'vn-1',
  metadata: { name: 'vn-prod' },
  spec: { ipv4Cidr: '10.0.0.0/16' },
  status: { state: VirtualNetworkState.READY },
} as VirtualNetwork;

const mockSubnet = {
  id: 'subnet-1',
  metadata: { name: 'subnet-a' },
  spec: { ipv4Cidr: '10.0.1.0/24', virtualNetwork: { id: 'vn-1' } },
  status: { state: SubnetState.READY },
} as Subnet;

const VirtualNetworkDetails = () => {
  const { id } = useParams<{ id: string }>();
  return <div>{`virtual network details ${id ?? ''}`}</div>;
};

const renderPage = (
  fixtures: MockApiFixtures = {},
  transportOverrides: MockTransportOverrides = {},
) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/subnets/:id" element={<SubnetDetailPage />} />
      <Route path="/networking/virtual-networks/:id" element={<VirtualNetworkDetails />} />
      <Route path="/networking/virtual-networks" element={<div>virtual networks list</div>} />
    </Routes>,
    {
      routerEntries: ['/networking/subnets/subnet-1'],
      apiFixtures: {
        virtualNetworks: [mockVirtualNetwork],
        subnets: [mockSubnet],
        ...fixtures,
      },
      transportOverrides,
    },
  );

describe('SubnetDetailPage', () => {
  it('renders the subnet details and a link to its virtual network', async () => {
    renderPage();

    expect(await screen.findByRole('heading', { name: 'subnet-a' })).toBeInTheDocument();
    expect(screen.getByText('10.0.1.0/24')).toBeInTheDocument();
    expect((await screen.findAllByRole('button', { name: 'vn-prod' })).length).toBeGreaterThan(0);
  });

  it('deletes the subnet and returns to the parent virtual network', async () => {
    let deleteRequest: SubnetsDeleteRequest | undefined;
    const { user } = renderPage(
      {},
      {
        onSubnetDelete: (req) => {
          deleteRequest = req;
          return create(SubnetsDeleteResponseSchema);
        },
      },
    );

    await screen.findByRole('heading', { name: 'subnet-a' });
    await user.click(screen.getByRole('button', { name: /^Delete$/ }));
    expect(screen.getByRole('heading', { name: /Delete subnet-a\?/ })).toBeInTheDocument();

    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/ }));

    await waitFor(() => expect(deleteRequest?.id).toBe('subnet-1'));
    expect(await screen.findByText('virtual network details vn-1')).toBeInTheDocument();
  });

  it('keeps the modal open and shows the server error when deletion fails', async () => {
    const { user } = renderPage(
      {},
      {
        onSubnetDelete: () => {
          throw new ConnectError('cannot delete subnet: in use', Code.FailedPrecondition);
        },
      },
    );

    await screen.findByRole('heading', { name: 'subnet-a' });
    await user.click(screen.getByRole('button', { name: /^Delete$/ }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/ }));

    expect(await screen.findByText('Failed to delete subnet')).toBeInTheDocument();
    expect(screen.getByText(/cannot delete subnet: in use/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Delete subnet-a\?/ })).toBeInTheDocument();
  });

  it('does not delete when Cancel is clicked', async () => {
    const onSubnetDelete = vi.fn();
    const { user } = renderPage({}, { onSubnetDelete });

    await screen.findByRole('heading', { name: 'subnet-a' });
    await user.click(screen.getByRole('button', { name: /^Delete$/ }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /Cancel/ }));

    expect(onSubnetDelete).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('disables Delete until the subnet is loaded', async () => {
    let resolveSubnetGet: ((value: { object?: Subnet }) => void) | undefined;
    const pendingSubnetGet = new Promise<{ object?: Subnet }>((resolve) => {
      resolveSubnetGet = resolve;
    });

    renderPage({}, { onSubnetGet: () => pendingSubnetGet });

    expect(screen.getByRole('button', { name: /^Delete$/ })).toHaveAttribute(
      'aria-disabled',
      'true',
    );

    resolveSubnetGet?.({ object: mockSubnet });
    await screen.findByRole('heading', { name: 'subnet-a' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^Delete$/ })).not.toHaveAttribute(
        'aria-disabled',
        'true',
      ),
    );
  });

  it('disables Delete for a default subnet', async () => {
    renderPage({
      subnets: [
        {
          ...mockSubnet,
          metadata: {
            ...mockSubnet.metadata,
            labels: { ...mockSubnet.metadata?.labels, 'osac.openshift.io/default': 'true' },
          },
        } as Subnet,
      ],
    });

    await screen.findByRole('heading', { name: 'subnet-a' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^Delete$/ })).toHaveAttribute(
        'aria-disabled',
        'true',
      ),
    );
  });
});
