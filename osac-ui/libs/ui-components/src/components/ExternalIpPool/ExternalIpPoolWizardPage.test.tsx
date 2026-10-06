import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ExternalIPPoolsCreateRequest,
  ExternalIPPoolsCreateResponseSchema,
  type Tenant,
  TenantState,
} from '@osac/types/private';

import { ExternalIpPoolWizardPage } from './ExternalIpPoolWizardPage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const LIST_PATH = '/admin/infrastructure/external-ip-pools';
const DETAILS_PATH = `${LIST_PATH}/new-external-ip-pool-1`;

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    // useBlocker requires a data router; this harness renders under a plain
    // MemoryRouter, so LeaveFormConfirmation's blocking behavior is stubbed out.
    useBlocker: () => ({ state: 'unblocked' as const }),
  };
});

const makeTenant = (id: string, name: string): Tenant =>
  ({
    id,
    metadata: { name },
    spec: { domains: [`${name}.example.com`] },
    status: { state: TenantState.SYNCED },
  }) as Tenant;

const defaultTenants = [makeTenant('t-1', 'acme'), makeTenant('t-2', 'globex')];

const renderCreatePage = (overrides?: MockTransportOverrides, tenants: Tenant[] = defaultTenants) =>
  renderWithProviders(<ExternalIpPoolWizardPage />, {
    transportOverrides: overrides,
    apiFixtures: { tenants },
  });

const clickNext = async (user: UserEvent) => {
  const [next] = screen.getAllByRole('button', { name: 'Next' });
  await user.click(next);
};

const fillPoolStep = async (user: UserEvent, name: string, cidr: string) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), name);
  await user.type(screen.getByRole('textbox', { name: 'CIDR 1' }), cidr);
};

const fillTenantStep = async (user: UserEvent, tenantName: string) => {
  await screen.findByRole('heading', { name: 'Tenant' });
  const tenantToggle = await waitFor(() => {
    const toggle = screen.getByRole('button', {
      name: (_accessibleName, element) => element.id === 'external-ip-pool-tenant',
    });
    expect(toggle).not.toBeDisabled();
    return toggle;
  });
  await user.click(tenantToggle);
  await user.click(screen.getByRole('option', { name: tenantName }));
};

const fillValidWizard = async (user: UserEvent) => {
  await fillPoolStep(user, 'prod-v4', '192.168.1.0/24');
  await clickNext(user);
  await fillTenantStep(user, 'acme');
  await clickNext(user);
  await screen.findByRole('heading', { name: 'Review' });
};

describe('ExternalIpPoolWizardPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  describe('create wizard', () => {
    it('renders the page title, breadcrumb, and pool step', () => {
      renderCreatePage();

      expect(screen.getByRole('heading', { name: 'Create external IP pool' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'External IP pools' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'External IP pool' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'CIDR 1' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Next' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    });

    it('advances through tenant and review, then submits the expected payload', async () => {
      let capturedRequest: ExternalIPPoolsCreateRequest | undefined;
      const { user } = renderCreatePage({
        onExternalIPPoolCreate: (req) => {
          capturedRequest = req;
          return create(ExternalIPPoolsCreateResponseSchema, {
            object: { id: 'new-external-ip-pool-1' },
          });
        },
      });

      await fillValidWizard(user);

      expect(screen.getByText('prod-v4')).toBeInTheDocument();
      expect(screen.getByText('192.168.1.0/24')).toBeInTheDocument();
      expect(screen.getByText('acme')).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(DETAILS_PATH);
      });

      expect(capturedRequest?.object?.metadata?.name).toBe('prod-v4');
      expect(capturedRequest?.object?.metadata?.tenant).toBe('t-1');
      expect(capturedRequest?.object?.spec?.cidrs).toEqual(['192.168.1.0/24']);
    }, 15000);

    it('shows the auto-selected tenant name on Review when only one tenant exists', async () => {
      const { user } = renderCreatePage(undefined, [makeTenant('t-1', 'acme')]);

      await fillPoolStep(user, 'prod-v4', '192.168.1.0/24');
      await clickNext(user);
      await screen.findByRole('heading', { name: 'Tenant' });
      await waitFor(() => {
        expect(
          screen.getByRole('button', {
            name: (_accessibleName, element) => element.id === 'external-ip-pool-tenant',
          }),
        ).toHaveTextContent('acme');
      });
      await clickNext(user);

      expect(await screen.findByRole('heading', { name: 'Review' })).toBeInTheDocument();
      expect(screen.getByText('acme')).toBeInTheDocument();
    });

    it('trims CIDR values in the create payload', async () => {
      let capturedRequest: ExternalIPPoolsCreateRequest | undefined;
      const { user } = renderCreatePage({
        onExternalIPPoolCreate: (req) => {
          capturedRequest = req;
          return create(ExternalIPPoolsCreateResponseSchema, {
            object: { id: 'new-external-ip-pool-1' },
          });
        },
      });

      await fillPoolStep(user, 'prod-v4', '  192.168.1.0/24  ');
      await clickNext(user);
      await fillTenantStep(user, 'acme');
      await clickNext(user);
      await screen.findByRole('heading', { name: 'Review' });
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(DETAILS_PATH);
      });
      expect(capturedRequest?.object?.spec?.cidrs).toEqual(['192.168.1.0/24']);
    }, 15000);

    it('blocks advancing past External IP pool for an invalid name', async () => {
      const onExternalIPPoolCreate = vi.fn();
      const { user } = renderCreatePage({ onExternalIPPoolCreate });

      await fillPoolStep(user, 'Invalid_Name', '192.168.1.0/24');
      await clickNext(user);

      expect(
        await screen.findByText(
          'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
        ),
      ).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();
    });

    it('adds and removes CIDR rows, keeping at least one', async () => {
      const { user } = renderCreatePage();

      expect(screen.getByRole('textbox', { name: 'CIDR 1' })).toBeInTheDocument();
      expect(screen.queryByRole('textbox', { name: 'CIDR 2' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Remove CIDR 1' })).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Add CIDR' }));

      expect(screen.getByRole('textbox', { name: 'CIDR 2' })).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Remove CIDR 2' }));

      expect(screen.queryByRole('textbox', { name: 'CIDR 2' })).not.toBeInTheDocument();
    });

    it('blocks advancing past External IP pool for a malformed CIDR', async () => {
      const onExternalIPPoolCreate = vi.fn();
      const { user } = renderCreatePage({ onExternalIPPoolCreate });

      await fillPoolStep(user, 'prod-v4', 'not-a-cidr');
      await clickNext(user);

      expect(await screen.findByText('Invalid IPv4 CIDR notation')).toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();
    });

    it('blocks advancing past Tenant when no tenant is selected', async () => {
      const onExternalIPPoolCreate = vi.fn();
      const { user } = renderCreatePage({ onExternalIPPoolCreate });

      await fillPoolStep(user, 'prod-v4', '192.168.1.0/24');
      await clickNext(user);
      await screen.findByRole('heading', { name: 'Tenant' });
      await clickNext(user);

      expect(await screen.findByText('Tenant is required')).toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();
    });

    it('warns when there are no registered tenants', async () => {
      const { user } = renderCreatePage(undefined, []);

      await fillPoolStep(user, 'prod-v4', '192.168.1.0/24');
      await clickNext(user);

      expect(await screen.findByText('No registered tenants')).toBeInTheDocument();
      expect(
        screen.getByText('Register a tenant before creating and assigning an external IP pool.'),
      ).toBeInTheDocument();
      expect(
        screen.getByRole('button', {
          name: (_accessibleName, element) => element.id === 'external-ip-pool-tenant',
        }),
      ).toBeDisabled();
    });

    it('shows a fetch error instead of the empty-tenant warning when listing tenants fails', async () => {
      const { user } = renderCreatePage({
        onTenantList: () => {
          throw new ConnectError('tenants unavailable', Code.Unavailable);
        },
      });

      await fillPoolStep(user, 'prod-v4', '192.168.1.0/24');
      await clickNext(user);

      expect(await screen.findByText('Failed to fetch tenants')).toBeInTheDocument();
      expect(screen.getByText('tenants unavailable')).toBeInTheDocument();
      expect(screen.queryByText('No registered tenants')).not.toBeInTheDocument();
      expect(
        screen.getByRole('button', {
          name: (_accessibleName, element) => element.id === 'external-ip-pool-tenant',
        }),
      ).toBeDisabled();
    });

    it('shows a form-level error and does not navigate when the name already exists', async () => {
      const { user } = renderCreatePage({
        onExternalIPPoolCreate: () => {
          throw new ConnectError('External IP pool name already exists', Code.AlreadyExists);
        },
      });

      await fillValidWizard(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Failed to create resource')).toBeInTheDocument();
      });
      expect(screen.getByText('External IP pool name already exists')).toBeInTheDocument();
      expect(mockNavigate).not.toHaveBeenCalled();
    }, 15000);

    it('navigates back to the list on cancel', async () => {
      const { user } = renderCreatePage();

      await user.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(mockNavigate).toHaveBeenCalledWith(LIST_PATH);
    });
  });
});
