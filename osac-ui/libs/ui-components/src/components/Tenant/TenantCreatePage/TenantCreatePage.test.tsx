import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { TenantsCreateRequest, TenantsCreateResponse } from '@osac/types/private';

import TenantCreatePage from './TenantCreatePage';
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

const renderPage = (overrides?: MockTransportOverrides) =>
  renderWithProviders(<TenantCreatePage />, {
    transportOverrides: overrides,
  });

describe('TenantCreatePage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  const goToBillingStep = async (user: ReturnType<typeof renderPage>['user']) => {
    await user.type(screen.getByRole('textbox', { name: 'Tenant name' }), 'acme-corp');
    await user.type(
      screen.getByRole('textbox', { name: 'Primary email domain' }),
      'acme.example.com',
    );
    await user.click(screen.getByRole('button', { name: 'Next' }));
  };

  const fillValidForm = async (user: ReturnType<typeof renderPage>['user']) => {
    await goToBillingStep(user);
    await user.click(screen.getByRole('button', { name: 'Next' }));
  };

  it('renders the register tenant page and wizard steps', () => {
    renderPage();

    expect(screen.getByRole('heading', { name: 'Register tenant' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Tenants' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /General/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Billing/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Review/ })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Tenant name' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('shows the optional external billing id field on the billing step', async () => {
    const { user } = renderPage();

    await goToBillingStep(user);

    expect(
      screen.getByText(
        'Enter an optional external billing ID for this tenant. You can leave it blank and add it later.',
      ),
    ).toBeInTheDocument();

    const billingId = screen.getByRole('textbox', { name: 'External billing ID' });
    expect(billingId).toHaveAttribute('placeholder', 'e.g. A00078421');
  });

  it('reviews the entered values before registering', async () => {
    const { user } = renderPage();

    await goToBillingStep(user);
    await user.type(screen.getByRole('textbox', { name: 'External billing ID' }), 'A00078421');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      screen.getByText('Review your tenant and billing settings before registering.'),
    ).toBeInTheDocument();
    expect(screen.getByText('acme-corp')).toBeInTheDocument();
    expect(screen.getByText('acme.example.com')).toBeInTheDocument();
    expect(screen.getByText('A00078421')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Register tenant' })).toBeInTheDocument();
  });

  it('rejects an external billing id over 256 characters with a field error', async () => {
    const { user } = renderPage();

    await goToBillingStep(user);
    await user.type(screen.getByRole('textbox', { name: 'External billing ID' }), 'A'.repeat(257));
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(
      await screen.findByText('External billing ID must be 256 characters or fewer'),
    ).toBeInTheDocument();
    expect(
      screen.queryByText('Review your tenant and billing settings before registering.'),
    ).not.toBeInTheDocument();
  });

  it('reviews an unset external billing id as not set', async () => {
    const { user } = renderPage();

    await fillValidForm(user);

    expect(screen.getByText('Not set — can add later')).toBeInTheDocument();
  });

  it('sends the domains and the external billing id on create', async () => {
    const onCreate = vi.fn(
      (_req: TenantsCreateRequest): TenantsCreateResponse =>
        ({
          object: { id: 'new-tenant-1', metadata: { name: 'acme-corp' } },
        }) as unknown as TenantsCreateResponse,
    );
    const { user } = renderPage({ onTenantCreate: onCreate });

    await goToBillingStep(user);
    await user.type(screen.getByRole('textbox', { name: 'External billing ID' }), 'A00078421');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Register tenant' }));

    await waitFor(() => {
      expect(onCreate).toHaveBeenCalled();
    });
    const request = onCreate.mock.calls[0][0];
    expect(request.object?.metadata?.name).toBe('acme-corp');
    expect(request.object?.spec?.domains).toEqual(['acme.example.com']);
    expect(request.object?.spec?.externalBillingId).toBe('A00078421');
  });

  it('omits the external billing id when it is left blank', async () => {
    const onCreate = vi.fn(
      (_req: TenantsCreateRequest): TenantsCreateResponse =>
        ({
          object: { id: 'new-tenant-1', metadata: { name: 'acme-corp' } },
        }) as unknown as TenantsCreateResponse,
    );
    const { user } = renderPage({ onTenantCreate: onCreate });

    await fillValidForm(user);
    await user.click(screen.getByRole('button', { name: 'Register tenant' }));

    await waitFor(() => {
      expect(onCreate).toHaveBeenCalled();
    });
    const request = onCreate.mock.calls[0][0];
    expect(request.object?.metadata?.name).toBe('acme-corp');
    expect(request.object?.spec?.domains).toEqual(['acme.example.com']);
    expect(request.object?.spec?.externalBillingId).toBeUndefined();
  });

  it('shows validation error when the tenant name is empty', async () => {
    const { user } = renderPage();

    await user.type(
      screen.getByRole('textbox', { name: 'Primary email domain' }),
      'acme.example.com',
    );
    await user.click(screen.getByRole('button', { name: 'Next' }));

    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('shows validation error for an invalid primary domain', async () => {
    const { user } = renderPage();

    await user.type(screen.getByRole('textbox', { name: 'Tenant name' }), 'acme');
    await user.type(screen.getByRole('textbox', { name: 'Primary email domain' }), 'not-a-domain');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    await waitFor(() => {
      expect(screen.getByText('Must be a valid domain (e.g. example.com)')).toBeInTheDocument();
    });
  });

  it('adds and removes additional domain fields', async () => {
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Add domain' }));
    expect(screen.getByRole('textbox', { name: 'Domain 1' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Add domain' }));
    expect(screen.getByRole('textbox', { name: 'Domain 2' })).toBeInTheDocument();

    const removeButtons = screen.getAllByRole('button', { name: 'Remove domain' });
    await user.click(removeButtons[1]);

    expect(screen.getByRole('textbox', { name: 'Domain 1' })).toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: 'Domain 2' })).not.toBeInTheDocument();
  });

  it('rejects the same domain twice', async () => {
    const { user } = renderPage();

    await user.type(screen.getByRole('textbox', { name: 'Tenant name' }), 'acme');
    await user.type(
      screen.getByRole('textbox', { name: 'Primary email domain' }),
      'acme.example.com',
    );
    await user.click(screen.getByRole('button', { name: 'Add domain' }));
    await user.type(screen.getByRole('textbox', { name: 'Domain 1' }), 'acme.example.com');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(await screen.findByText('Domains must be unique')).toBeInTheDocument();
    expect(
      screen.queryByText('Review your tenant and billing settings before registering.'),
    ).not.toBeInTheDocument();
  });

  it('shows break-glass credential dialog on successful create', async () => {
    const { user } = renderPage();

    await fillValidForm(user);
    await user.click(screen.getByRole('button', { name: 'Register tenant' }));

    await waitFor(() => {
      expect(screen.getByText('Break-glass credentials')).toBeInTheDocument();
    });
    expect(
      screen.getByText('Save these credentials now — they cannot be retrieved later.'),
    ).toBeInTheDocument();

    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByDisplayValue('break-glass-admin')).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue('temp-password-123')).toBeInTheDocument();
  });

  it('navigates to the tenant list after dismissing the credential dialog', async () => {
    const { user } = renderPage();

    await fillValidForm(user);
    await user.click(screen.getByRole('button', { name: 'Register tenant' }));

    await waitFor(() => {
      expect(screen.getByText('Break-glass credentials')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'I have saved the credentials' }));

    expect(mockNavigate).toHaveBeenCalledWith('/admin/tenants');
  });

  it('shows error alert when create fails', async () => {
    const { user } = renderPage({
      onTenantCreate: () => {
        throw new ConnectError('Tenant name already exists', Code.AlreadyExists);
      },
    });

    await fillValidForm(user);
    await user.click(screen.getByRole('button', { name: 'Register tenant' }));

    await waitFor(() => {
      expect(screen.getByText('Unable to register tenant')).toBeInTheDocument();
    });
    expect(screen.getByText('Tenant name already exists')).toBeInTheDocument();
  });

  it('requires an added domain row to be filled', async () => {
    const { user } = renderPage();

    await user.type(screen.getByRole('textbox', { name: 'Tenant name' }), 'acme');
    await user.type(
      screen.getByRole('textbox', { name: 'Primary email domain' }),
      'acme.example.com',
    );
    await user.click(screen.getByRole('button', { name: 'Add domain' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));

    await waitFor(() => {
      expect(screen.getByText('Domain is required')).toBeInTheDocument();
    });
    expect(
      screen.queryByText('Review your tenant and billing settings before registering.'),
    ).not.toBeInTheDocument();
  });

  it('keeps the primary domain first and the additional domains in order', async () => {
    const onCreate = vi.fn(
      (_req: TenantsCreateRequest): TenantsCreateResponse =>
        ({
          object: { id: 'new-tenant-1', metadata: { name: 'acme' } },
        }) as unknown as TenantsCreateResponse,
    );
    const { user } = renderPage({ onTenantCreate: onCreate });

    await user.type(screen.getByRole('textbox', { name: 'Tenant name' }), 'acme');
    await user.type(
      screen.getByRole('textbox', { name: 'Primary email domain' }),
      'acme.example.com',
    );
    await user.click(screen.getByRole('button', { name: 'Add domain' }));
    await user.type(screen.getByRole('textbox', { name: 'Domain 1' }), 'eu.example.com');
    await user.click(screen.getByRole('button', { name: 'Add domain' }));
    await user.type(screen.getByRole('textbox', { name: 'Domain 2' }), 'us.example.com');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Register tenant' }));

    await waitFor(() => {
      expect(onCreate).toHaveBeenCalled();
    });
    const request = onCreate.mock.calls[0][0];
    expect(request.object?.spec?.domains).toEqual([
      'acme.example.com',
      'eu.example.com',
      'us.example.com',
    ]);
  });

  it('navigates back to the tenant list on cancel', async () => {
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(mockNavigate).toHaveBeenCalledWith('/admin/tenants');
  });

  it('navigates back to the tenant list via the breadcrumb', async () => {
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Tenants' }));

    expect(mockNavigate).toHaveBeenCalledWith('/admin/tenants');
  });
});
