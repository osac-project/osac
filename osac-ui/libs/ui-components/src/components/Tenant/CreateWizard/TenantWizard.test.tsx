import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Tenant, TenantsUpdateRequest, TenantsUpdateResponse } from '@osac/types/private';

import TenantWizard, { type TenantWizardStartStep } from './TenantWizard';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const tenant = {
  id: 'tenant-1',
  metadata: { name: 'acme-corp' },
  spec: { domains: ['acme.example.com'], externalBillingId: 'A00078421' },
} as Tenant;

const tenantWithoutDomains = {
  id: 'shared',
  metadata: { name: 'shared' },
  spec: {},
} as Tenant;

const makeUpdateMock = (value: Tenant) =>
  vi.fn(
    (_req: TenantsUpdateRequest): TenantsUpdateResponse =>
      ({ object: value }) as unknown as TenantsUpdateResponse,
  );

const renderWizard = (
  overrides?: MockTransportOverrides,
  startStep?: TenantWizardStartStep,
  resource: Tenant = tenant,
) =>
  renderWithProviders(<TenantWizard tenant={resource} startStep={startStep} />, {
    transportOverrides: overrides,
  });

const changeBillingId = async (user: UserEvent, value: string) => {
  const input = screen.getByRole('textbox', { name: 'External billing ID' });
  await user.clear(input);
  if (value) {
    await user.type(input, value);
  }
};

describe('TenantWizard in edit mode', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('starts on the billing step when asked to', () => {
    renderWizard(undefined, 'billing');

    expect(screen.getByRole('textbox', { name: 'External billing ID' })).toHaveValue('A00078421');
  });

  it('keeps the tenant name read-only', async () => {
    const { user } = renderWizard(undefined, 'billing');

    await user.click(screen.getByRole('button', { name: 'General' }));

    expect(screen.getByRole('textbox', { name: 'Tenant name' })).toBeDisabled();
  });

  it('reviews the change before saving', async () => {
    const { user } = renderWizard(undefined, 'billing');

    await changeBillingId(user, 'B0001');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(screen.getByText('Review your changes before saving.')).toBeInTheDocument();
    expect(screen.getByText('A00078421')).toBeInTheDocument();
    expect(screen.getByText('B0001')).toBeInTheDocument();
    expect(screen.queryByText('Primary email domain')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  });

  it('saves the id with a mask limited to the changed field', async () => {
    const onUpdate = makeUpdateMock(tenant);
    const { user } = renderWizard({ onTenantUpdate: onUpdate }, 'billing');

    await changeBillingId(user, 'B0001');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalled();
    });
    const request: TenantsUpdateRequest = onUpdate.mock.calls[0][0];
    expect(request.object?.id).toBe('tenant-1');
    expect(request.object?.metadata).toBeUndefined();
    expect(request.object?.spec?.externalBillingId).toBe('B0001');
    expect(request.updateMask?.paths).toEqual(['spec.external_billing_id']);
  });

  it('clears the id by omitting it from the masked write', async () => {
    const onUpdate = makeUpdateMock(tenant);
    const { user } = renderWizard({ onTenantUpdate: onUpdate }, 'billing');

    await changeBillingId(user, '');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalled();
    });
    const request: TenantsUpdateRequest = onUpdate.mock.calls[0][0];
    expect(request.object?.spec?.externalBillingId).toBeUndefined();
    expect(request.updateMask?.paths).toEqual(['spec.external_billing_id']);
  });

  it('saves a tenant that has no email domain', async () => {
    const onUpdate = makeUpdateMock(tenantWithoutDomains);
    const { user } = renderWizard({ onTenantUpdate: onUpdate }, 'billing', tenantWithoutDomains);

    await user.type(screen.getByRole('textbox', { name: 'External billing ID' }), 'A0001');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalled();
    });
    const request: TenantsUpdateRequest = onUpdate.mock.calls[0][0];
    expect(request.object?.spec?.externalBillingId).toBe('A0001');
    expect(request.updateMask?.paths).toEqual(['spec.external_billing_id']);
    expect(mockNavigate).toHaveBeenCalledWith('/admin/tenants');
  });

  it('sends only the domains path when a domain changes', async () => {
    const onUpdate = makeUpdateMock(tenant);
    const { user } = renderWizard({ onTenantUpdate: onUpdate });

    const primaryDomain = screen.getByRole('textbox', { name: 'Primary email domain' });
    await user.clear(primaryDomain);
    await user.type(primaryDomain, 'acme-corp.example.com');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalled();
    });
    const request: TenantsUpdateRequest = onUpdate.mock.calls[0][0];
    expect(request.object?.spec?.domains).toEqual(['acme-corp.example.com']);
    expect(request.updateMask?.paths).toEqual(['spec.domains']);
  });

  it('disables saving when nothing changed', async () => {
    const onUpdate = vi.fn();
    const { user } = renderWizard({ onTenantUpdate: onUpdate }, 'billing');

    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      screen.getByText('No changes yet. Update a step to see a before-and-after summary here.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('shows the save error from the API', async () => {
    const { user } = renderWizard(
      {
        onTenantUpdate: () => {
          throw new ConnectError('billing id is already in use', Code.InvalidArgument);
        },
      },
      'billing',
    );

    await changeBillingId(user, 'B0001');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(screen.getByText('Unable to save tenant')).toBeInTheDocument();
    });
    expect(screen.getByText('billing id is already in use')).toBeInTheDocument();
  });
});
