import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { type ComputeInstanceTemplate, ComputeInstanceTemplates, ServiceTier } from '@osac/types';
import type { Tenant } from '@osac/types/private';

import CatalogGeneralStep, {
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from './CatalogGeneralStep';
import type { UserRole } from '../../../shellTypes';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../hooks/use-session', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-admin',
    username: 'testuser',
    tenantId: 'tenant-1',
    enabledServices: [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
  })),
}));

const { useSession } = await import('../../../hooks/use-session');

const makeSession = (role: UserRole) => ({
  role,
  username: 'testuser',
  tenantId: 'tenant-1',
  enabledServices: [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
  userTheme: 'system' as const,
  resolvedTheme: 'light' as const,
  setUserTheme: vi.fn(),
  userContrast: 'system' as const,
  resolvedContrast: 'glass' as const,
  setUserContrast: vi.fn(),
  projects: [],
  setProjects: vi.fn(),
});

const makeTenant = (id: string, name: string): Tenant => ({ id, metadata: { name } }) as Tenant;

const makeTemplate = (id: string, name: string): ComputeInstanceTemplate =>
  ({ id, metadata: { name } }) as ComputeInstanceTemplate;

const renderStep = (
  isEdit: boolean,
  tenants: Tenant[] = [],
  computeInstanceTemplates: ComputeInstanceTemplate[] = [],
) =>
  renderWithProviders(
    <Formik initialValues={catalogGeneralValues()} onSubmit={() => {}}>
      <CatalogGeneralStep
        isEdit={isEdit}
        templateService={ComputeInstanceTemplates}
        templateFieldId="catalog-item-template"
      />
    </Formik>,
    { apiFixtures: { tenants, computeInstanceTemplates } },
  );

describe('CatalogGeneralStep', () => {
  beforeEach(() => {
    vi.mocked(useSession).mockReturnValue(makeSession('tenant-admin'));
  });

  it('does not show the General step description', () => {
    renderStep(false);

    expect(
      screen.queryByText(
        'Define the name, scope, description, and provisioning template for this catalog item.',
      ),
    ).not.toBeInTheDocument();
  });

  it('uses a compact description field', () => {
    renderStep(false);

    expect(screen.getByRole('textbox', { name: 'Description' })).toHaveAttribute('rows', '4');
  });

  it('shows the provisioning template helper text', () => {
    renderStep(false);

    expect(
      screen.getByText(
        'The selected template is the provisioning template used to create resources from this catalog item.',
      ),
    ).toBeInTheDocument();
  });

  describe('for a tenant-admin session', () => {
    it('shows the project field and no scope cards or tenant field', () => {
      renderStep(false);

      expect(screen.getByRole('button', { name: 'Project' })).toBeInTheDocument();
      expect(screen.queryByRole('radiogroup', { name: 'Scope' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Tenant' })).not.toBeInTheDocument();
    });
  });

  describe('for an admin session', () => {
    beforeEach(() => {
      vi.mocked(useSession).mockReturnValue(makeSession('admin'));
    });

    it('shows both helper sentences and the scope cards, defaulting to Tenant scoped', () => {
      renderStep(false, [makeTenant('tenant-1', 'Tenant One')]);

      expect(
        screen.getByText(
          'Assign the catalog item to a tenant (or make it global), then enter a name. Names are unique per tenant.',
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          'Global resources are available to all tenants. Tenant-scoped resources must be assigned to one tenant.',
        ),
      ).toBeInTheDocument();
      expect(screen.getByText('Global')).toBeInTheDocument();
      expect(screen.getByText('Available to all tenants on the platform')).toBeInTheDocument();
      expect(screen.getByText('Tenant scoped')).toBeInTheDocument();
      expect(screen.getByText('Assigned to one tenant')).toBeInTheDocument();
      const tenantScopedCard = screen
        .getByText('Tenant scoped')
        .closest('#catalog-item-scope-tenant');
      expect(tenantScopedCard).toHaveClass('pf-m-selected');
      expect(screen.getByRole('radio', { name: /Tenant scoped/ })).toHaveClass(
        'pf-v6-screen-reader',
      );
      const selectedLabel = screen.getByText('Selected').closest('.pf-v6-c-label');
      expect(selectedLabel).toHaveClass('pf-m-filled');
      expect(selectedLabel).not.toHaveClass('pf-m-blue');
      expect(screen.getByRole('button', { name: 'Tenant' })).toBeInTheDocument();
    });

    it('hides the tenant field after choosing Global', async () => {
      const { user } = renderStep(false, [makeTenant('tenant-1', 'Tenant One')]);

      await user.click(screen.getByText('Global'));

      await waitFor(() => {
        expect(screen.queryByRole('button', { name: 'Tenant' })).not.toBeInTheDocument();
      });
    });

    it('shows the tenant field after choosing Tenant scoped again', async () => {
      const { user } = renderStep(false, [makeTenant('tenant-1', 'Tenant One')]);

      await user.click(screen.getByText('Global'));
      await user.click(screen.getByText('Tenant scoped'));

      expect(screen.getByRole('button', { name: 'Tenant' })).toBeInTheDocument();
    });

    it('does not show the project field', () => {
      renderStep(false);

      expect(screen.queryByRole('button', { name: 'Project' })).not.toBeInTheDocument();
    });
  });

  describe('edit mode', () => {
    it('disables the name field', () => {
      renderStep(true, [], [makeTemplate('template-1', 'Template One')]);

      expect(screen.getByRole('textbox', { name: 'Name' })).toBeDisabled();
    });

    it('disables the project field for a tenant-admin session', () => {
      renderStep(true);

      // ProjectField renders a disabled Select toggle button for its field.
      expect(screen.getByRole('button', { name: 'Project' })).toBeDisabled();
    });

    it('disables the scope cards for an admin session', () => {
      vi.mocked(useSession).mockReturnValue(makeSession('admin'));
      renderStep(true, [makeTenant('tenant-1', 'Tenant One')]);

      const tenantScopedCard = screen
        .getByText('Tenant scoped')
        .closest('#catalog-item-scope-tenant');
      expect(tenantScopedCard).toHaveClass('pf-m-disabled');
      expect(screen.getByRole('button', { name: 'Tenant' })).toBeInTheDocument();
    });

    it('shows the edit-mode template helper text', () => {
      renderStep(true, [], [makeTemplate('template-1', 'Template One')]);

      expect(
        screen.getByText(
          'The provisioning template cannot be changed after the catalog item is created.',
        ),
      ).toBeInTheDocument();
    });
  });
});

describe('catalogGeneralValuesFromItem', () => {
  const baseItem = {
    metadata: {
      name: 'my-item',
      description: 'My description',
      project: 'team-a',
      tenant: 'tenant-1',
    },
    template: { id: 'template-1' },
  };

  it('derives a tenant scope and populates fields from a tenant-scoped item', () => {
    expect(catalogGeneralValuesFromItem(baseItem)).toEqual({
      scope: 'tenant',
      metadata: {
        name: 'my-item',
        project: 'team-a',
        description: 'My description',
        tenant: { id: 'tenant-1', name: '' },
      },
      template: { id: 'template-1', name: '' },
    });
  });

  it('derives a global scope and an empty tenant from a global item', () => {
    const item = { ...baseItem, metadata: { ...baseItem.metadata, tenant: 'shared' } };

    const values = catalogGeneralValuesFromItem(item);

    expect(values.scope).toBe('global');
    expect(values.metadata.tenant).toEqual({ id: '', name: '' });
  });

  it('returns empty defaults when metadata and template are absent', () => {
    expect(catalogGeneralValuesFromItem({})).toEqual(catalogGeneralValues());
  });
});
