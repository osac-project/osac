import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ComputeInstanceCatalogItem,
  ComputeInstanceCatalogItemSchema,
  type ComputeInstanceTemplate,
  type DiskImage,
  type InstanceType,
} from '@osac/types';
import type { Tenant } from '@osac/types/private';

import ComputeCatalogItemWizard from './ComputeCatalogItemWizard';
import type { UserRole } from '../../../../shellTypes';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
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
  resolvedTheme: 'light' as const,
  setUserTheme: vi.fn(),
  userContrast: 'system' as const,
  resolvedContrast: 'glass' as const,
  setUserContrast: vi.fn(),
  projects: [],
  setProjects: vi.fn(),
});

const makeTemplate = (id: string, name: string): ComputeInstanceTemplate =>
  ({ id, metadata: { name } }) as ComputeInstanceTemplate;

const makeInstanceType = (id: string, name: string): InstanceType =>
  ({ id, metadata: { name }, spec: { vcpus: 4, memoryGib: 8 } }) as InstanceType;

const makeDiskImage = (id: string, name: string): DiskImage =>
  ({ id, metadata: { name } }) as DiskImage;

const TEMPLATE = makeTemplate('template-1', 'VM template');
const INSTANCE_TYPE = makeInstanceType('it-1', 'standard-4-8');
const DISK_IMAGE = makeDiskImage('image-1', 'rhel-9');

const apiFixtures = {
  computeInstanceTemplates: [TEMPLATE],
  instanceTypes: [INSTANCE_TYPE],
  diskImages: [DISK_IMAGE],
};

const renderWizard = (
  item?: ComputeInstanceCatalogItem,
  transportOverrides: Record<string, unknown> = {},
) =>
  renderWithProviders(<ComputeCatalogItemWizard item={item} />, {
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
  await user.click(await screen.findByText('VM template'));
};

describe('ComputeCatalogItemWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
    vi.mocked(useSession).mockReturnValue(makeSession('tenant-admin'));
  });

  it('shows Details, Compute configuration, Storage, Access & Initial run, and Review steps', () => {
    renderWizard();

    const nav = screen.getByRole('navigation', { name: 'Create compute catalog item steps' });
    expect(within(nav).getByText('Details')).toBeInTheDocument();
    expect(within(nav).getByText('Compute configuration')).toBeInTheDocument();
    expect(within(nav).getByText('Storage')).toBeInTheDocument();
    expect(within(nav).getByText('Access & Initial run')).toBeInTheDocument();
    expect(within(nav).getByText('Review')).toBeInTheDocument();
    expect(within(nav).queryByText('Networking')).not.toBeInTheDocument();
  });

  it('shows the Compute configuration step description and no run strategy field', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText(
        'Set the default disk image and instance type for virtual machines created from this catalog item. Leave fields Not set if tenants should choose when creating a virtual machine.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Compute configuration' })).toBeInTheDocument();
    expect(screen.queryByText(/run strategy/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Enter value' })).not.toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(1);
  });

  it('shows guest OS family cards and selects a guest OS family', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    const guestOsGroup = await screen.findByRole('radiogroup', {
      name: 'Select guest operating system',
    });
    const diskImageSection = screen.getByRole('group', { name: 'Disk image' });
    expect(within(diskImageSection).getByRole('switch', { name: 'Editable' })).toBeInTheDocument();
    expect(screen.getByLabelText('Select the image reference')).toBeInTheDocument();
    expect(
      within(guestOsGroup).getByText('Linux-based guest operating systems'),
    ).toBeInTheDocument();
    expect(within(guestOsGroup).getByRole('radio', { name: /^Linux/ })).toBeChecked();

    await user.click(within(guestOsGroup).getByText('Microsoft Windows'));

    expect(within(guestOsGroup).getByRole('radio', { name: /^Microsoft Windows/ })).toBeChecked();
    expect(within(guestOsGroup).getByText('Selected')).toBeInTheDocument();
  });

  it('shows the Storage step description', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText(
        'Optionally set boot disk size, storage tier, and additional disk defaults for this catalog item. Leave fields Not set if tenants should choose when creating a virtual machine.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Boot storage tier' })).toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(2);
  });

  it('shows the Access & Initial run step with user data choices', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText('Add optional user data, or skip this step to continue.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Enter value' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Select secret' })).toBeInTheDocument();
  });

  describe('Review step', () => {
    const goToReview = async (user: ReturnType<typeof renderWizard>['user'], name = 'my-item') => {
      await fillGeneralStep(user, name);
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
    };

    it('ends with the purple "Starts as unpublished" alert with the tenant-admin body', async () => {
      const { user } = renderWizard();

      await goToReview(user);

      expect(await screen.findByText('Starts as unpublished')).toBeInTheDocument();
      expect(
        screen.getByText(
          'New catalog items are saved as unpublished. Publish from the catalog when you are ready for users in your tenant to use this offering.',
        ),
      ).toBeInTheDocument();
    });

    it('shows the cloud provider admin body for an admin session', async () => {
      vi.mocked(useSession).mockReturnValue(makeSession('admin'));
      const { user } = renderWithProviders(<ComputeCatalogItemWizard />, {
        apiFixtures: {
          ...apiFixtures,
          tenants: [{ id: 'tenant-1', metadata: { name: 'Tenant One' } } as Tenant],
        },
      });

      await user.click(screen.getByText('Global'));
      await fillGeneralStep(user, 'my-item');
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));

      expect(
        await screen.findByText(
          'New catalog items are saved as unpublished. Publish from the catalog when you are ready for tenants to use this offering.',
        ),
      ).toBeInTheDocument();
    });
  });

  it('blocks Next on Compute configuration when a locked field is empty', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    const switches = await screen.findAllByRole('switch');
    // Disk image is the first governed field rendered on Compute configuration.
    await user.click(switches[0]);
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText('Fix the highlighted errors before continuing.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        'Set the default disk image and instance type for virtual machines created from this catalog item. Leave fields Not set if tenants should choose when creating a virtual machine.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('Disk image is required when locked')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next' })).toBeInTheDocument();
  });

  it('creates the catalog item with empty editable fields omitted from the request', async () => {
    const onCreate = vi.fn((req: { object?: unknown }) => ({
      object: { ...(req.object as object), id: 'catalog-item-1' },
    }));
    const { user } = renderWizard(undefined, { onComputeInstanceCatalogItemCreate: onCreate });

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    const request = onCreate.mock.calls[0][0] as {
      object?: { fields?: Record<string, unknown>; metadata?: object };
    };
    expect(request.object?.metadata).toMatchObject({ name: 'my-item' });
    expect(Object.keys(request.object?.fields ?? {}).filter((key) => key !== '$typeName')).toEqual(
      [],
    );
    expect(mockNavigate).toHaveBeenCalledWith('/catalog/vm/catalog-item-1');
  });

  it('sends an editable field value as editable.default_value', async () => {
    const onCreate = vi.fn((req: { object?: unknown }) => ({
      object: { ...(req.object as object), id: 'catalog-item-1' },
    }));
    const { user } = renderWizard(undefined, { onComputeInstanceCatalogItemCreate: onCreate });

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));

    await user.type(screen.getByRole('textbox', { name: 'User data' }), 'echo hi');

    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    const request = onCreate.mock.calls[0][0] as {
      object?: { fields?: { userData?: unknown } };
    };
    expect(request.object?.fields?.userData).toMatchObject({
      behavior: { case: 'editable', value: { defaultValue: 'echo hi' } },
    });
  });

  it('stays on Review with entered values after a failed submit', async () => {
    const onCreate = vi.fn(() => {
      throw new ConnectError('boom', Code.Internal);
    });
    const { user } = renderWizard(undefined, { onComputeInstanceCatalogItemCreate: onCreate });

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(await screen.findByText('Starts as unpublished')).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  describe('edit mode', () => {
    const existingItem = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-item-1',
      metadata: { name: 'existing-item', description: 'An existing item', tenant: 'test-tenant' },
      title: 'Existing item',
      template: { id: 'template-1' },
      published: true,
      fields: {
        userData: { behavior: { case: 'locked', value: 'cloud-init' } },
      },
    });

    it('prefills from the item and submits an update without the template', async () => {
      const onUpdate = vi.fn((req: { object?: unknown }) => ({
        object: { ...(req.object as object), id: 'catalog-item-1' },
      }));
      const { user } = renderWizard(existingItem, { onComputeInstanceCatalogItemUpdate: onUpdate });

      expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('existing-item');

      await user.click(screen.getByRole('button', { name: 'Next' }));
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
      expect(mockNavigate).toHaveBeenCalledWith('/catalog/vm/catalog-item-1');
    });
  });
});
