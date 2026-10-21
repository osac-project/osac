import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItemSchema,
  type BareMetalInstanceTemplate,
  type BareMetalInstanceType,
  type DiskImage,
} from '@osac/types';
import type { Tenant } from '@osac/types/private';

import BareMetalCatalogItemWizard from './BareMetalCatalogItemWizard';
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

const TEMPLATE = {
  id: 'template-1',
  metadata: { name: 'Bare metal template' },
} as BareMetalInstanceTemplate;
const INSTANCE_TYPE = {
  id: 'instance-type-1',
  metadata: { name: 'acme-1tb' },
} as BareMetalInstanceType;
const DISK_IMAGE = { id: 'image-1', metadata: { name: 'rhel-9' } } as DiskImage;

const apiFixtures = {
  bareMetalInstanceTemplates: [TEMPLATE],
  publicBaremetalInstanceTypes: [INSTANCE_TYPE],
  diskImages: [DISK_IMAGE],
};

const renderWizard = (
  item?: BareMetalInstanceCatalogItem,
  transportOverrides: Record<string, unknown> = {},
) =>
  renderWithProviders(<BareMetalCatalogItemWizard item={item} />, {
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
  await user.click(await screen.findByText('Bare metal template'));
};

const goToReview = async (user: ReturnType<typeof renderWizard>['user']) => {
  await fillGeneralStep(user, 'my-bare-metal-item');
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.click(screen.getByRole('button', { name: 'Next' }));
};

describe('BareMetalCatalogItemWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
    vi.mocked(useSession).mockReturnValue(makeSession('tenant-admin'));
  });

  it('shows General, Configuration, and Review without Storage or Networking', () => {
    renderWizard();

    const nav = screen.getByRole('navigation', { name: 'Create bare metal catalog item steps' });
    expect(within(nav).getByText('General')).toBeInTheDocument();
    expect(within(nav).getByText('Configuration')).toBeInTheDocument();
    expect(within(nav).getByText('Review')).toBeInTheDocument();
    expect(within(nav).queryByText('Storage')).not.toBeInTheDocument();
    expect(within(nav).queryByText('Networking')).not.toBeInTheDocument();
  });

  it('shows the configuration description and excludes run strategy', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText(
        'Optionally set instance type, disk image, and user data defaults for this catalog item. Leave fields Not set if tenants should choose when creating a bare metal instance.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Instance type')).toBeInTheDocument();
    expect(screen.getByLabelText('Disk image')).toBeInTheDocument();
    expect(screen.getByLabelText('User data')).toBeInTheDocument();
    expect(screen.queryByText(/run strategy/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/SSH public key/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/network attachment/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/external IP/i)).not.toBeInTheDocument();
  });

  it('blocks Configuration Next when a locked field is empty', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click((await screen.findAllByRole('switch'))[0]);
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(await screen.findByText('Instance type is required when locked')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Configuration' })).toBeInTheDocument();
  });

  it('ends Review with the tenant-admin unpublished alert', async () => {
    const { user } = renderWizard();

    await goToReview(user);

    expect(
      await screen.findByText('Review the configured defaults before creating this catalog item.'),
    ).toBeInTheDocument();
    expect(screen.getByText('Starts as unpublished')).toBeInTheDocument();
    expect(
      screen.getByText(
        'New catalog items are saved as unpublished. Publish from the catalog when you are ready for users in your tenant to use this offering.',
      ),
    ).toBeInTheDocument();
  });

  it('shows the cloud provider admin unpublished alert body', async () => {
    vi.mocked(useSession).mockReturnValue(makeSession('admin'));
    const { user } = renderWithProviders(<BareMetalCatalogItemWizard />, {
      apiFixtures: {
        ...apiFixtures,
        tenants: [{ id: 'tenant-1', metadata: { name: 'Tenant One' } } as Tenant],
      },
    });

    await user.click(screen.getByText('Global'));
    await goToReview(user);

    expect(
      await screen.findByText(
        'New catalog items are saved as unpublished. Publish from the catalog when you are ready for tenants to use this offering.',
      ),
    ).toBeInTheDocument();
  });

  it('creates an unpublished item and omits empty editable fields', async () => {
    const onCreate = vi.fn((req: { object?: unknown }) => ({
      object: { ...(req.object as object), id: 'catalog-item-1' },
    }));
    const { user } = renderWizard(undefined, { onBareMetalInstanceCatalogItemCreate: onCreate });

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
    expect(mockNavigate).toHaveBeenCalledWith('/catalog/bm/catalog-item-1');
  });

  it('stays on Review after a failed submit', async () => {
    const onCreate = vi.fn(() => {
      throw new ConnectError('boom', Code.Internal);
    });
    const { user } = renderWizard(undefined, { onBareMetalInstanceCatalogItemCreate: onCreate });

    await goToReview(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(screen.getByText('Starts as unpublished')).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('prefills edit mode and submits an update without the template', async () => {
    const existingItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-item-1',
      metadata: { name: 'existing-item', description: 'An existing item', tenant: 'test-tenant' },
      template: { id: 'template-1' },
      published: true,
      fields: {
        instanceType: { behavior: { case: 'locked', value: { name: 'acme-1tb' } } },
        diskImage: {
          behavior: {
            case: 'editable',
            value: { defaultValue: { id: 'image-1', name: 'rhel-9' } },
          },
        },
        userData: { behavior: { case: 'locked', value: 'echo hi' } },
      },
    });
    const onUpdate = vi.fn((req: { object?: unknown }) => ({
      object: { ...(req.object as object), id: 'catalog-item-1' },
    }));
    const { user } = renderWizard(existingItem, {
      onBareMetalInstanceCatalogItemUpdate: onUpdate,
    });

    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('existing-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Instance type' })).toHaveTextContent('acme-1tb'),
    );
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
    expect(mockNavigate).toHaveBeenCalledWith('/catalog/bm/catalog-item-1');
  });
});
