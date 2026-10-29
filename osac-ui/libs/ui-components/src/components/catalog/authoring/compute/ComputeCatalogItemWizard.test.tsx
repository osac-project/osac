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
  StorageProtocol,
  type StorageTier,
  StorageTierSchema,
  StorageTierState,
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

vi.mock('@osac/ui-components/hooks/use-session.tsx', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/hooks/use-session.tsx')>();
  return {
    ...actual,
    useSession: vi.fn(() => ({
      role: 'tenant-admin',
      username: 'test-user',
      tenantId: 'test-tenant',
    })),
  };
});

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
const STORAGE_TIER = create(StorageTierSchema, {
  id: 'tier-1',
  metadata: { name: 'fast' },
  spec: { protocol: StorageProtocol.BLOCK },
  status: { state: StorageTierState.ACTIVE },
});

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
    expect(screen.getByRole('combobox', { name: 'Boot storage tier' })).toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(2);
  });

  it('shows the Access & Initial run step with text-only user data', async () => {
    const { user } = renderWizard();

    await fillGeneralStep(user, 'my-item');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText('Add optional user data, or skip this step to continue.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'User data' })).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Enter value' })).not.toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Select secret' })).not.toBeInTheDocument();
  });

  describe('Review step', () => {
    const goToReview = async (user: ReturnType<typeof renderWizard>['user'], name = 'my-item') => {
      await fillGeneralStep(user, name);
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
    };

    it('shows the UX review sections, tenant scope details, status, and governance labels', async () => {
      const { user } = renderWizard();

      await goToReview(user);

      expect(await screen.findByRole('heading', { name: 'Review and create' })).toBeInTheDocument();
      expect(
        screen.getByText(
          'Confirm your settings before creating the catalog item. It will be saved as unpublished until you publish it. Click a section title to edit.',
        ),
      ).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Details' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Compute configuration' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Storage' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
      expect(screen.getByText('Name')).toBeInTheDocument();
      expect(screen.getByText('Provisioning template')).toBeInTheDocument();
      expect(screen.getByText('Guest operating system')).toBeInTheDocument();
      expect(screen.getByText('Disk image')).toBeInTheDocument();
      expect(screen.getByText('Instance type')).toBeInTheDocument();
      expect(screen.getByText('Boot disk size')).toBeInTheDocument();
      expect(screen.getByText('Boot storage tier')).toBeInTheDocument();
      expect(screen.getByText('Additional disks')).toBeInTheDocument();
      expect(screen.getByText('Cloud-init')).toBeInTheDocument();
      expect(screen.getByText('Linux')).toBeInTheDocument();
      expect(screen.getAllByText('Not set').length).toBeGreaterThan(0);
      expect(screen.getByText('Unpublished')).toBeInTheDocument();
      expect(screen.getAllByText('Editable')).toHaveLength(5);
      expect(screen.getByRole('button', { name: 'Create catalog item' })).toBeInTheDocument();
      expect(screen.queryByText('Project')).not.toBeInTheDocument();
    });

    it('shows the global scope row for an administrator', async () => {
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

      expect(await screen.findByText('Global')).toBeInTheDocument();
      expect(screen.getByText('Scope')).toBeInTheDocument();
      expect(screen.getByText('Unpublished')).toBeInTheDocument();
    });

    it('navigates from each review section title back to its wizard step', async () => {
      const { user } = renderWizard();

      await goToReview(user);

      const returnToReview = async (nextCount: number) => {
        for (let index = 0; index < nextCount; index += 1) {
          await user.click(screen.getByRole('button', { name: 'Next' }));
        }
      };

      await user.click(screen.getByRole('button', { name: 'Edit Details' }));
      expect(await screen.findByRole('textbox', { name: 'Name' })).toBeInTheDocument();
      await returnToReview(4);

      await user.click(screen.getByRole('button', { name: 'Edit Compute configuration' }));
      expect(
        await screen.findByRole('heading', { name: 'Compute configuration' }),
      ).toBeInTheDocument();
      await returnToReview(3);

      await user.click(screen.getByRole('button', { name: 'Edit Storage' }));
      expect(await screen.findByRole('heading', { name: 'Storage' })).toBeInTheDocument();
      await returnToReview(2);

      await user.click(screen.getByRole('button', { name: 'Edit Access & Initial run' }));
      expect(
        await screen.findByRole('heading', { name: 'Access & Initial run' }),
      ).toBeInTheDocument();
    });

    it('shows configured Cloud-init without exposing its inline contents', async () => {
      const { user } = renderWizard();

      await fillGeneralStep(user, 'my-item');
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.type(screen.getByRole('textbox', { name: 'User data' }), 'secret inline value');
      await user.click(screen.getByRole('button', { name: 'Next' }));

      expect(await screen.findByText('Configured')).toBeInTheDocument();
      expect(screen.queryByText('secret inline value')).not.toBeInTheDocument();
    });

    it('formats configured storage defaults as concise disk-set summaries', async () => {
      const { user } = renderWithProviders(<ComputeCatalogItemWizard />, {
        apiFixtures: { ...apiFixtures, publicStorageTiers: [STORAGE_TIER] as StorageTier[] },
      });

      await fillGeneralStep(user, 'my-item');
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.clear(screen.getByRole('spinbutton', { name: 'Boot disk size' }));
      await user.type(screen.getByRole('spinbutton', { name: 'Boot disk size' }), '40');
      await user.click(screen.getByRole('button', { name: 'Add disk' }));
      await user.click(await screen.findByRole('button', { name: 'Storage tier' }));
      await user.click(screen.getByRole('option', { name: 'fast' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));

      expect(await screen.findByText('40 GiB')).toBeInTheDocument();
      expect(screen.getByText('Disk set 1: 30 GiB · fast')).toBeInTheDocument();
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
    await user.click(screen.getByRole('button', { name: 'Create catalog item' }));

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
    await user.click(screen.getByRole('button', { name: 'Create catalog item' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    const request = onCreate.mock.calls[0][0] as {
      object?: { fields?: { userDataSource?: unknown } };
    };
    expect(request.object?.fields?.userDataSource).toMatchObject({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'inline', value: 'echo hi' } } },
      },
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
    await user.click(screen.getByRole('button', { name: 'Create catalog item' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Review and create' })).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  describe('edit mode', () => {
    const existingItem = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-item-1',
      metadata: { name: 'existing-item', tenant: 'test-tenant' },
      title: 'Existing item',
      template: { id: 'template-1' },
      published: true,
      fields: {},
    });

    it('shows no changes and disables Save changes when the edit is pristine', async () => {
      const { user } = renderWizard(existingItem);

      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));

      expect(
        await screen.findByRole('heading', { name: 'Review your changes before saving' }),
      ).toBeInTheDocument();
      expect(
        screen.getByText('No changes yet. Update a step to see a before-and-after summary here.'),
      ).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    });

    it('prefills from the item and submits changed fields without the template', async () => {
      const onUpdate = vi.fn((req: { object?: unknown }) => ({
        object: { ...(req.object as object), id: 'catalog-item-1' },
      }));
      const { user } = renderWizard(existingItem, { onComputeInstanceCatalogItemUpdate: onUpdate });

      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.type(screen.getByRole('textbox', { name: 'User data' }), 'updated cloud-init');
      await user.click(screen.getByRole('button', { name: 'Next' }));

      expect(screen.getByText('Configured')).toBeInTheDocument();
      expect(screen.queryByText('updated cloud-init')).not.toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Save changes' }));

      await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
      const request = onUpdate.mock.calls[0][0] as {
        object?: { id?: string };
        updateMask?: { paths?: string[] };
      };
      expect(request.object?.id).toBe('catalog-item-1');
      expect(request.updateMask?.paths).toContain(
        'fields.user_data_source.editable.default_value.inline',
      );
      expect(request.updateMask?.paths).not.toContain('template');
      expect(request.updateMask?.paths).not.toContain('metadata.name');
      expect(request.updateMask?.paths).not.toContain('metadata.tenant');
      expect(mockNavigate).toHaveBeenCalledWith('/catalog/vm/catalog-item-1');
    });

    it('stays on the edit review after a failed submission', async () => {
      const onUpdate = vi.fn(() => {
        throw new ConnectError('boom', Code.Internal);
      });
      const { user } = renderWizard(existingItem, { onComputeInstanceCatalogItemUpdate: onUpdate });

      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.type(screen.getByRole('textbox', { name: 'User data' }), 'updated cloud-init');
      await user.click(screen.getByRole('button', { name: 'Next' }));
      await user.click(screen.getByRole('button', { name: 'Save changes' }));

      expect(await screen.findByText('Failed to edit resource')).toBeInTheDocument();
      expect(
        await screen.findByRole('heading', { name: 'Review your changes before saving' }),
      ).toBeInTheDocument();
      expect(mockNavigate).not.toHaveBeenCalled();
    });
  });
});
