import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ClusterCatalogItemSchema,
  ClusterVersionState,
  type ClusterCatalogItem,
  type ClusterTemplate,
  type ClusterVersion,
  type BareMetalInstanceType,
} from '@osac/types';

import ClusterCatalogItemWizard from './ClusterCatalogItemWizard';
import type { UserRole } from '../../../../shellTypes';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => mockNavigate };
});

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-admin',
    username: 'test-user',
    tenantId: 'test-tenant',
  })),
}));

const { useSession } = await import('@osac/ui-components/hooks/use-session.tsx');

const makeSession = (role: UserRole) => ({
  role,
  username: 'test-user',
  tenantId: 'test-tenant',
  enabledServices: [],
  userTheme: 'system' as const,
  userContrast: 'system' as const,
  resolvedTheme: 'light' as const,
  resolvedContrast: 'glass' as const,
  setUserTheme: vi.fn(),
  setUserContrast: vi.fn(),
  projects: [],
  setProjects: vi.fn(),
});

const TEMPLATE = { id: 'template-1', metadata: { name: 'Cluster template' } } as ClusterTemplate;
const VERSION = {
  id: 'version-1',
  metadata: { name: '4-17-0' },
  spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
} as ClusterVersion;
const HOST_TYPE = { id: 'bmit-1', metadata: { name: 'worker' } } as BareMetalInstanceType;

const apiFixtures = {
  clusterTemplates: [TEMPLATE],
  clusterVersions: [VERSION],
  publicBaremetalInstanceTypes: [HOST_TYPE],
};

const renderWizard = (
  item?: ClusterCatalogItem,
  transportOverrides: Record<string, unknown> = {},
) =>
  renderWithProviders(<ClusterCatalogItemWizard item={item} />, {
    apiFixtures,
    transportOverrides: transportOverrides as never,
  });

const fillGeneralStep = async (user: ReturnType<typeof renderWizard>['user'], name: string) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), name);
  const templateInput = within(
    screen.getByRole('button', { name: 'Provisioning template' }).closest('.pf-v6-c-menu-toggle') ??
      document.body,
  ).getByRole('combobox');
  await user.click(templateInput);
  await user.click(await screen.findByText('Cluster template'));
};

const goToReview = async (user: ReturnType<typeof renderWizard>['user']) => {
  await fillGeneralStep(user, 'my-cluster-item');
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.click(screen.getByRole('button', { name: 'Next' }));
};

describe('ClusterCatalogItemWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
    vi.mocked(useSession).mockReturnValue(makeSession('tenant-admin'));
  });

  it('shows General, Configuration, Networking, and Review steps without excluded fields', () => {
    renderWizard();

    const nav = screen.getByRole('navigation', { name: 'Create cluster catalog item steps' });
    expect(within(nav).getByText('General')).toBeInTheDocument();
    expect(within(nav).getByText('Configuration')).toBeInTheDocument();
    expect(within(nav).getByText('Networking')).toBeInTheDocument();
    expect(within(nav).getByText('Review')).toBeInTheDocument();
    expect(within(nav).queryByText('Storage')).not.toBeInTheDocument();
  });

  it('shows service-specific descriptions and omits infrastructure controls', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    expect(
      await screen.findByText(
        'Optionally set cluster version and node set defaults for this catalog item. Leave fields Not set if tenants should choose when creating a cluster.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Version')).toBeInTheDocument();
    expect(screen.queryByText(/run strategy/i)).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Next' }));
    expect(
      await screen.findByText(
        'Optionally set pod CIDR and service CIDR defaults for this catalog item. Leave fields Not set if tenants should choose when creating a cluster.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Pod CIDR')).toBeInTheDocument();
    expect(screen.getByLabelText('Service CIDR')).toBeInTheDocument();
    expect(screen.queryByText('Infrastructure Networking')).not.toBeInTheDocument();
    expect(screen.queryByText(/SSH public key/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Pull secret/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/external IP/i)).not.toBeInTheDocument();
  });

  it('ends review with the tenant-admin unpublished alert', async () => {
    const { user } = renderWizard();

    await goToReview(user);

    expect(await screen.findByText('Starts as unpublished')).toBeInTheDocument();
    expect(
      screen.getByText(
        'New catalog items are saved as unpublished. Publish from the catalog when you are ready for users in your tenant to use this offering.',
      ),
    ).toBeInTheDocument();
  });

  it('blocks Next when a locked node-set field is empty', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    const switches = await screen.findAllByRole('switch');
    await user.click(switches[1]);
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText('Fix the highlighted errors before continuing.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText('At least one node set is required when this field is locked'),
    ).toBeInTheDocument();
  });

  it('creates an unpublished item and omits empty editable fields', async () => {
    const onCreate = vi.fn((req: { object?: unknown }) => ({
      object: { ...(req.object as object), id: 'catalog-item-1' },
    }));
    const { user } = renderWizard(undefined, { onClusterCatalogItemCreate: onCreate });

    await goToReview(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    const request = onCreate.mock.calls[0][0] as {
      object?: { fields?: Record<string, unknown>; published?: boolean };
    };
    expect(request.object?.published).toBe(false);
    expect(Object.keys(request.object?.fields ?? {}).filter((key) => key !== '$typeName')).toEqual(
      [],
    );
    expect(mockNavigate).toHaveBeenCalledWith('/catalog/cluster/catalog-item-1');
  });

  it('stays on Review with entered values after a failed submit', async () => {
    const onCreate = vi.fn(() => {
      throw new ConnectError('boom', Code.Internal);
    });
    const { user } = renderWizard(undefined, { onClusterCatalogItemCreate: onCreate });

    await goToReview(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(screen.getByText('Starts as unpublished')).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('prefills edit mode and submits an update without the template', async () => {
    const existingItem = create(ClusterCatalogItemSchema, {
      id: 'catalog-item-1',
      metadata: { name: 'existing-item', description: 'An existing item', tenant: 'test-tenant' },
      title: 'Existing item',
      template: { id: 'template-1' },
      published: true,
      fields: {
        version: { behavior: { case: 'locked', value: { name: '4-17-0' } } },
      },
    });
    const onUpdate = vi.fn((req: { object?: unknown }) => ({
      object: { ...(req.object as object), id: 'catalog-item-1' },
    }));
    const { user } = renderWizard(existingItem, { onClusterCatalogItemUpdate: onUpdate });

    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('existing-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Edit' }));

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    const request = onUpdate.mock.calls[0][0] as {
      object?: { id?: string };
      updateMask?: { paths?: string[] };
    };
    expect(request.object?.id).toBe('catalog-item-1');
    expect(request.updateMask?.paths).toContain('metadata.description');
    expect(request.updateMask?.paths).not.toContain('template');
    expect(request.updateMask?.paths).not.toContain('metadata.name');
    expect(request.updateMask?.paths).not.toContain('metadata.tenant');
    expect(mockNavigate).toHaveBeenCalledWith('/catalog/cluster/catalog-item-1');
  });
});
