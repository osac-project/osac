import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { BareMetalInstance } from '@osac/types';
import { BareMetalInstanceState } from '@osac/types';

import { BareMetalTable } from './BareMetalTable';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-user',
    username: 'test-user',
    tenantId: 'test-tenant',
  })),
}));

const instance: BareMetalInstance = {
  $typeName: 'osac.public.v1.BareMetalInstance',
  id: 'bmi-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'worker-01',
    displayName: '',
    description: '',
    annotations: {},
    labels: {},
    creator: 'admin',
    project: 'default',
    tenant: 'tenant-1',
    version: 1,
  },
  spec: {
    $typeName: 'osac.public.v1.BareMetalInstanceSpec',
    catalogItem: {
      $typeName: 'osac.public.v1.BareMetalInstanceCatalogItemReference',
      id: 'catalog-1',
      name: 'RHEL 9 bare metal',
      project: 'default',
      shared: false,
    },
    templateParameters: {},
    networkAttachments: [],
    restartTrigger: 0n,
  },
  status: {
    $typeName: 'osac.public.v1.BareMetalInstanceStatus',
    state: BareMetalInstanceState.RUNNING,
    conditions: [],
    restartTrigger: 0n,
    networkAttachmentStatuses: [],
  },
};

describe('BareMetalTable', () => {
  it('renders bare metal instances in a table', () => {
    renderWithProviders(<BareMetalTable instances={[instance]} />);

    expect(screen.getByRole('grid', { name: 'Bare metal instances' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'worker-01' })).toHaveAttribute(
      'href',
      '/bare-metal/bmi-1',
    );
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9 bare metal')).toBeInTheDocument();
  });
});
