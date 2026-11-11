import { Route, Routes, useLocation } from 'react-router-dom';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { VirtualNetwork } from '@osac/types';
import { VirtualNetworkState } from '@osac/types';

import AttachNatGatewayWizardPage from './AttachNatGatewayWizardPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const virtualNetworks = [
  {
    id: 'vn-1',
    metadata: { name: 'vn-prod', project: 'proj-a' },
    spec: { ipv4Cidr: '10.0.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
] as VirtualNetwork[];

const CurrentLocation = () => {
  const location = useLocation();
  return <p>{`At ${location.pathname}${location.search}`}</p>;
};

const renderPage = (entry: string) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/networking/virtual-networks/:id/nat-gateway/attach"
        element={<AttachNatGatewayWizardPage />}
      />
      <Route
        path="/networking/nat-gateways/create/:id"
        element={<AttachNatGatewayWizardPage fromNatGatewaysList />}
      />
      <Route path="*" element={<CurrentLocation />} />
    </Routes>,
    {
      apiFixtures: { virtualNetworks, externalIps: [], natGateways: [] },
      routerEntries: [entry],
      session: { role: 'tenant-admin' },
    },
  );

describe('AttachNatGatewayWizardPage', () => {
  it("returns to the virtual network's NAT gateway tab when started there", async () => {
    const { user } = renderPage('/networking/virtual-networks/vn-1/nat-gateway/attach');

    expect(await screen.findByRole('heading', { name: 'Create NAT gateway' })).toBeInTheDocument();
    const breadcrumb = within(screen.getByRole('navigation', { name: 'Breadcrumb' }));
    expect(breadcrumb.getByRole('link', { name: 'Virtual networks' })).toBeInTheDocument();
    expect(breadcrumb.getByRole('link', { name: 'vn-prod' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1?tab=nat-gateway',
    );

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(
      await screen.findByText('At /networking/virtual-networks/vn-1?tab=nat-gateway'),
    ).toBeInTheDocument();
  });

  it('returns to the NAT gateways list when started there', async () => {
    const { user } = renderPage('/networking/nat-gateways/create/vn-1');

    expect(await screen.findByRole('heading', { name: 'Create NAT gateway' })).toBeInTheDocument();
    const breadcrumb = within(screen.getByRole('navigation', { name: 'Breadcrumb' }));
    expect(breadcrumb.getByRole('link', { name: 'NAT gateways' })).toHaveAttribute(
      'href',
      '/networking/nat-gateways',
    );

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByText('At /networking/nat-gateways')).toBeInTheDocument();
  });
});
