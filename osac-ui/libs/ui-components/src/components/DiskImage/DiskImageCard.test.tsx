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

import DiskImageCard from './DiskImageCard';
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
      architecture: overrides.architecture ?? [Architecture.AMD64, Architecture.ARM64],
      lifecycle: overrides.lifecycle ?? DiskImageLifecycle.AVAILABLE,
    },
  });

describe('DiskImageCard', () => {
  it('renders disk image details in a card', () => {
    renderWithProviders(
      <DiskImageCard
        diskImage={makeDiskImage({ id: 'di-1', name: 'fedora-41' })}
        tenants={[makeTenant('tenant-a', 'Acme Corp')]}
      />,
    );

    expect(screen.getByRole('link', { name: 'fedora-41' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/disk-images/di-1',
    );
    expect(screen.getByText('Linux')).toBeInTheDocument();
    expect(screen.getByText('amd64, arm64')).toBeInTheDocument();
    expect(screen.getByText('Available').closest('.pf-v6-c-label')).toHaveClass('pf-m-green');
    expect(screen.getByText('Created')).toBeInTheDocument();
  });

  it('shows Global scope when metadata.tenant is shared', () => {
    renderWithProviders(
      <DiskImageCard
        diskImage={makeDiskImage({ id: 'global-1', tenant: 'shared' })}
        tenants={[]}
      />,
    );

    expect(screen.getByText('Global')).toBeInTheDocument();
  });

  it('resolves tenant scope from the tenant list', () => {
    renderWithProviders(
      <DiskImageCard
        diskImage={makeDiskImage({ id: 'tenant-1', tenant: 'tenant-a' })}
        tenants={[makeTenant('tenant-a', 'Acme Corp')]}
      />,
    );

    expect(screen.getByText('Acme Corp')).toBeInTheDocument();
  });
});
