import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  Architecture,
  type DiskImage,
  DiskImageLifecycle,
  DiskImageSchema,
  GuestOSFamily,
} from '@osac/types';
import type { Tenant } from '@osac/types/private';

import DiskImageTable from './DiskImageTable';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeTenant = (id: string, name: string): Tenant =>
  ({
    id,
    metadata: { name },
  }) as Tenant;

const makeDiskImage = (overrides: {
  id: string;
  name?: string;
  tenant?: string;
  guestOsFamily?: GuestOSFamily;
  architecture?: Architecture[];
  lifecycle?: DiskImageLifecycle;
}): DiskImage =>
  create(DiskImageSchema, {
    id: overrides.id,
    metadata: {
      name: overrides.name ?? `disk-image-${overrides.id}`,
      tenant: overrides.tenant,
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      sourceRef: `quay.io/example/${overrides.id}:latest`,
      guestOsFamily: overrides.guestOsFamily ?? GuestOSFamily.GUEST_OS_FAMILY_LINUX,
      architecture: overrides.architecture ?? [Architecture.AMD64],
      lifecycle: overrides.lifecycle ?? DiskImageLifecycle.AVAILABLE,
    },
  });

const renderTableWithRoutes = (diskImages: DiskImage[], tenants: Tenant[] = []) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/admin/infrastructure/disk-images"
        element={<DiskImageTable diskImages={diskImages} tenants={tenants} />}
      />
      <Route
        path="/admin/infrastructure/disk-images/:id"
        element={<h1>Disk image detail page</h1>}
      />
    </Routes>,
    { routerEntries: ['/admin/infrastructure/disk-images'] },
  );

describe('DiskImageTable', () => {
  it('renders the required columns', () => {
    renderTableWithRoutes([
      makeDiskImage({
        id: 'global-1',
        architecture: [Architecture.AMD64, Architecture.ARM64],
      }),
    ]);

    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'Lifecycle',
      'Guest OS family',
      'Architecture',
      'Scope',
      'Created',
      '',
    ]);
    expect(screen.getByRole('link', { name: 'disk-image-global-1' })).toBeInTheDocument();
    expect(screen.getByText('amd64, arm64')).toBeInTheDocument();
    expect(screen.getByText('Available').closest('.pf-v6-c-label')).toHaveClass('pf-m-green');
    expect(screen.getByText('Linux')).toBeInTheDocument();
  });

  it('falls back to Unspecified for an architecture value not known to the frontend', () => {
    renderTableWithRoutes([
      makeDiskImage({ id: 'unknown-arch-1', architecture: [99 as Architecture] }),
    ]);

    expect(screen.getByText('Unspecified')).toBeInTheDocument();
  });

  it('shows Global scope when metadata.tenant is "shared"', () => {
    renderTableWithRoutes([makeDiskImage({ id: 'global-1', tenant: 'shared' })]);

    expect(screen.getByText('Global')).toBeInTheDocument();
  });

  it('shows the resolved tenant name for a tenant-scoped image', () => {
    renderTableWithRoutes(
      [makeDiskImage({ id: 'tenant-1', tenant: 'tenant-a' })],
      [makeTenant('tenant-a', 'Acme Corp')],
    );

    expect(screen.getByText('Acme Corp')).toBeInTheDocument();
  });

  it('falls back to the raw tenant id when the tenant is not found in the list', () => {
    renderTableWithRoutes([makeDiskImage({ id: 'tenant-1', tenant: 'tenant-a' })], []);

    expect(screen.getByText('tenant-a')).toBeInTheDocument();
  });

  it('navigates to the detail route when the name is clicked', async () => {
    const { user } = renderTableWithRoutes([makeDiskImage({ id: 'di-1' })]);

    await user.click(screen.getByRole('link', { name: 'disk-image-di-1' }));

    expect(screen.getByRole('heading', { name: 'Disk image detail page' })).toBeInTheDocument();
  });

  it("wires each row's actions menu to that row's own disk image", async () => {
    const { user } = renderTableWithRoutes([
      makeDiskImage({ id: 'available-1', lifecycle: DiskImageLifecycle.AVAILABLE }),
      makeDiskImage({ id: 'obsolete-1', lifecycle: DiskImageLifecycle.OBSOLETE }),
    ]);

    await user.click(screen.getByRole('button', { name: 'Actions for disk-image-available-1' }));
    expect(screen.getByRole('menuitem', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Reactivate' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Actions for disk-image-available-1' }));
    await user.click(screen.getByRole('button', { name: 'Actions for disk-image-obsolete-1' }));
    expect(screen.getByRole('menuitem', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Deprecate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Obsolete' })).not.toBeInTheDocument();
  });
});
