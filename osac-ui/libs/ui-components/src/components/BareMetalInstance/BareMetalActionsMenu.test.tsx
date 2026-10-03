import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  type BareMetalInstance,
  BareMetalInstanceState,
  BareMetalInstanceStatus,
} from '@osac/types';

import { BareMetalActionsMenu } from './BareMetalActionsMenu';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-user',
    username: 'test-user',
    tenantId: 'test-tenant',
    projects: [],
    setProjects: vi.fn(),
  })),
}));

const runningInstanceStatus: BareMetalInstanceStatus = {
  $typeName: 'osac.public.v1.BareMetalInstanceStatus',
  state: BareMetalInstanceState.RUNNING,
  conditions: [],
  restartTrigger: 0n,
  networkAttachmentStatuses: [
    {
      $typeName: 'osac.public.v1.BareMetalNetworkAttachmentStatus',
      interface: 'eth0',
      subnetRef: 'subnet-1',
      ipAddress: '10.0.0.14',
      primary: true,
    },
  ],
};

const runningInstance: BareMetalInstance = {
  $typeName: 'osac.public.v1.BareMetalInstance',
  id: 'bmi-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'bm-server-04',
    displayName: '',
    description: '',
    annotations: {},
    labels: {},
    creator: 'admin',
    project: 'ml-project',
    tenant: 'tenant-1',
    version: 1,
  },
  spec: {
    $typeName: 'osac.public.v1.BareMetalInstanceSpec',
    catalogItem: {
      $typeName: 'osac.public.v1.BareMetalInstanceCatalogItemReference',
      id: 'catalog-1',
      name: 'bare-metal-gpu-training-server',
      project: 'default',
      shared: false,
    },
    templateParameters: {},
    networkAttachments: [],
    restartTrigger: 0n,
  },
  status: runningInstanceStatus,
};

const openMenu = async (user: ReturnType<typeof renderWithProviders>['user']) => {
  await user.click(screen.getByRole('button', { name: 'Actions for bm-server-04' }));
};

describe('BareMetalActionsMenu', () => {
  it('opens the Connect via SSH modal from the actions menu', async () => {
    const { user } = renderWithProviders(<BareMetalActionsMenu instance={runningInstance} />);

    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Connect via SSH' }));

    expect(screen.getByRole('dialog', { name: 'Connect via SSH' })).toBeInTheDocument();
    expect(screen.getByText('10.0.0.14')).toBeInTheDocument();
    expect(screen.getByDisplayValue('ssh test-user@10.0.0.14')).toBeInTheDocument();
  });

  it('disables Connect via SSH when the instance is not running', async () => {
    const stoppedInstance: BareMetalInstance = {
      ...runningInstance,
      status: {
        ...runningInstanceStatus,
        state: BareMetalInstanceState.STOPPED,
      },
    };

    const { user } = renderWithProviders(<BareMetalActionsMenu instance={stoppedInstance} />);

    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Connect via SSH' })).toBeDisabled();
  });
});
