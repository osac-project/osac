import { Route, Routes, useParams, useSearchParams } from 'react-router-dom';
import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Tenant } from '@osac/types/private';

import TenantEditPage from './TenantEditPage';
import TenantExternalBillingId from './TenantExternalBillingId';
import { renderWithProviders } from '../../test-utils/TestProviders';

const tenant = {
  id: 'tenant-1',
  metadata: { name: 'acme-corp' },
  spec: { domains: ['acme.example.com'], externalBillingId: 'A00078421' },
} as Tenant;

const tenantWithoutBillingId = {
  id: 'tenant-2',
  metadata: { name: 'shared' },
  spec: {},
} as Tenant;

const EditStub = () => {
  const { id } = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  return (
    <>
      <h1>Edit route</h1>
      <p>id: {id}</p>
      <p>step: {searchParams.get('step') ?? 'none'}</p>
    </>
  );
};

const renderEditPage = (entry: string) =>
  renderWithProviders(
    <Routes>
      <Route path="/admin/tenants" element={<h1>Tenants</h1>} />
      <Route path="/admin/tenants/:id/edit" element={<TenantEditPage />} />
    </Routes>,
    { routerEntries: [entry], apiFixtures: { tenants: [tenant] } },
  );

const renderBillingId = (resource: Tenant) =>
  renderWithProviders(
    <Routes>
      <Route path="/" element={<TenantExternalBillingId tenant={resource} />} />
      <Route path="/admin/tenants/:id/edit" element={<EditStub />} />
    </Routes>,
    { routerEntries: ['/'] },
  );

describe('TenantEditPage', () => {
  it('prefills the billing step and the read-only tenant name from the deep link', async () => {
    const { user } = renderEditPage('/admin/tenants/tenant-1/edit?step=billing');

    expect(await screen.findByRole('textbox', { name: 'External billing ID' })).toHaveValue(
      'A00078421',
    );

    await user.click(screen.getByRole('button', { name: 'General' }));
    expect(screen.getByRole('textbox', { name: 'Tenant name' })).toBeDisabled();
  });

  it('renders the edit tenant header and breadcrumb', async () => {
    renderEditPage('/admin/tenants/tenant-1/edit');

    expect(await screen.findByRole('heading', { name: 'Edit tenant' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Tenants' })).toBeInTheDocument();
  });
});

describe('TenantExternalBillingId', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the stored id and opens the billing step for editing', async () => {
    const { user } = renderBillingId(tenant);

    expect(screen.getByText('A00078421')).toBeInTheDocument();

    await user.click(
      screen.getByRole('button', { name: 'Edit external billing ID for acme-corp' }),
    );

    expect(await screen.findByText('Edit route')).toBeInTheDocument();
    expect(screen.getByText('id: tenant-1')).toBeInTheDocument();
    expect(screen.getByText('step: billing')).toBeInTheDocument();
  });

  it('offers to add an id when the tenant has none', async () => {
    const { user } = renderBillingId(tenantWithoutBillingId);

    await user.click(screen.getByRole('button', { name: 'Add external billing ID for shared' }));

    expect(await screen.findByText('Edit route')).toBeInTheDocument();
    expect(screen.getByText('id: tenant-2')).toBeInTheDocument();
    expect(screen.getByText('step: billing')).toBeInTheDocument();
  });
});
