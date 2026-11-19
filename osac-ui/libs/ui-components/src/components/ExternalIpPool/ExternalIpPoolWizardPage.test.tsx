import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ExternalIPPoolsCreateRequest,
  ExternalIPPoolsCreateResponseSchema,
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

const renderCreatePage = (overrides?: MockTransportOverrides) =>
  renderWithProviders(<ExternalIpPoolWizardPage />, {
    transportOverrides: overrides,
  });

const clickNext = async (user: UserEvent) => {
  const [next] = screen.getAllByRole('button', { name: 'Next' });
  await user.click(next);
};

const fillPoolStep = async (user: UserEvent, name: string, cidr: string) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), name);
  await user.type(screen.getByRole('textbox', { name: 'CIDR' }), cidr);
};

const fillValidWizard = async (user: UserEvent) => {
  await fillPoolStep(user, 'prod-v4', '192.168.1.0/24');
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
      expect(screen.getByRole('textbox', { name: 'CIDR' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Next' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    });

    it('has no Tenant step, because pools are shared by all tenants', () => {
      renderCreatePage();

      expect(
        screen.getByText('The pool is shared by all tenants.', { exact: false }),
      ).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'External IP pool' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Tenant' })).not.toBeInTheDocument();
    });

    it('advances from the pool step to review, then submits the expected payload', async () => {
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
      expect(screen.queryByText('Tenant')).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(DETAILS_PATH);
      });

      expect(capturedRequest?.object?.metadata?.name).toBe('prod-v4');
      // The server places the pool in the shared tenant, so the request names none.
      expect(capturedRequest?.object?.metadata?.tenant).toBe('');
      expect(capturedRequest?.object?.spec?.cidrs).toEqual(['192.168.1.0/24']);
    }, 15000);

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

    it('offers exactly one CIDR and no Add CIDR action', () => {
      renderCreatePage();

      expect(screen.getByRole('textbox', { name: 'CIDR' })).toBeInTheDocument();
      expect(screen.queryByRole('textbox', { name: 'CIDR 2' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Add CIDR' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /Remove CIDR/ })).not.toBeInTheDocument();
    });

    it('blocks advancing past External IP pool for a malformed CIDR', async () => {
      const onExternalIPPoolCreate = vi.fn();
      const { user } = renderCreatePage({ onExternalIPPoolCreate });

      await fillPoolStep(user, 'prod-v4', 'not-a-cidr');
      await clickNext(user);

      expect(await screen.findByText('Invalid IPv4 CIDR notation')).toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();
    });

    it('shows a CIDR field error for a host address and advances after correction', async () => {
      const onExternalIPPoolCreate = vi.fn();
      const { user } = renderCreatePage({ onExternalIPPoolCreate });

      await fillPoolStep(user, 'prod-v4', '2.3.4.5/23');
      await clickNext(user);

      const cidrInput = screen.getByRole('textbox', { name: 'CIDR' });
      await screen.findByText('Use 2.3.4.0/23 instead. CIDRs must start at the network address.');
      expect(cidrInput).toHaveAttribute('aria-invalid', 'true');
      expect(cidrInput).toHaveAccessibleDescription(/Use 2\.3\.4\.0\/23 instead/);
      expect(screen.queryByRole('heading', { name: 'Review' })).not.toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();

      await user.clear(cidrInput);
      await user.type(cidrInput, '2.3.4.0/23');
      await clickNext(user);

      expect(await screen.findByRole('heading', { name: 'Review' })).toBeInTheDocument();
      expect(screen.getByText('2.3.4.0/23')).toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();
    });

    it('rejects an IPv6 CIDR', async () => {
      const onExternalIPPoolCreate = vi.fn();
      const { user } = renderCreatePage({ onExternalIPPoolCreate });

      await fillPoolStep(user, 'prod-v4', 'fd01::/48');
      await clickNext(user);

      expect(await screen.findByText('Invalid IPv4 CIDR notation')).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Review' })).not.toBeInTheDocument();
      expect(onExternalIPPoolCreate).not.toHaveBeenCalled();
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
