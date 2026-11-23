import { Route, Routes, useParams } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { VirtualNetworkCreatePage } from './VirtualNetworkCreatePage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const DetailProbe = () => {
  const { id } = useParams<{ id: string }>();
  return <div>{`detail:${id}`}</div>;
};

const renderPage = (transportOverrides = {}) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/virtual-networks/create" element={<VirtualNetworkCreatePage />} />
      <Route path="/networking/virtual-networks/:id" element={<DetailProbe />} />
    </Routes>,
    { routerEntries: ['/networking/virtual-networks/create'], transportOverrides },
  );

describe('VirtualNetworkCreatePage', () => {
  it('shows the project, name and IPv4 CIDR fields without a NetworkClass field', () => {
    renderPage();

    expect(screen.getByLabelText(/^Project/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^IPv4 CIDR/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/NetworkClass/i)).not.toBeInTheDocument();
  });

  it('reports the required fields and keeps the entered values', async () => {
    const { user } = renderPage();

    await user.type(screen.getByLabelText(/^Name/), 'vn-kept');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText(/CIDR is required/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Name/)).toHaveValue('vn-kept');
  });

  it('rejects an invalid CIDR inline and keeps the values', async () => {
    const { user } = renderPage();

    await user.type(screen.getByLabelText(/^Name/), 'vn-kept');
    await user.type(screen.getByLabelText(/^IPv4 CIDR/), '10.0.0.0/33');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText(/valid IPv4 CIDR/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Name/)).toHaveValue('vn-kept');
    expect(screen.getByLabelText(/^IPv4 CIDR/)).toHaveValue('10.0.0.0/33');
  });

  it('keeps a create error until the user edits a field', async () => {
    const { user } = renderPage({
      onVirtualNetworkCreate: () => {
        throw new Error('server rejected the create');
      },
    });

    await user.type(screen.getByLabelText(/^Name/), 'vn-fail');
    await user.type(screen.getByLabelText(/^IPv4 CIDR/), '10.12.0.0/16');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create virtual network')).toBeInTheDocument();
    // The error appears with the submitted values and must not clear itself.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.getByText('Failed to create virtual network')).toBeInTheDocument();

    await user.type(screen.getByLabelText(/^Name/), '-edited');
    await waitFor(() =>
      expect(screen.queryByText('Failed to create virtual network')).not.toBeInTheDocument(),
    );
  });

  it('submits with Enter in a text field', async () => {
    const { user } = renderPage();

    await user.type(screen.getByLabelText(/^Name/), 'vn-enter');
    const cidr = screen.getByLabelText(/^IPv4 CIDR/);
    await user.type(cidr, '10.11.0.0/16');
    await user.type(cidr, '{enter}');

    expect(await screen.findByText('detail:vn-created')).toBeInTheDocument();
  });

  it('creates the network and opens its details page', async () => {
    const { user } = renderPage();

    await user.type(screen.getByLabelText(/^Name/), 'vn-new');
    await user.type(screen.getByLabelText(/^IPv4 CIDR/), '10.10.0.0/16');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('detail:vn-created')).toBeInTheDocument();
  });
});
