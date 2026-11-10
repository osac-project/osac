import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { act, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import { type ServiceAccount, ServiceAccountSchema, ServiceAccountState } from '@osac/types';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { createMockConnectTransport } from '@osac/ui-components/test-utils/createMockConnectTransport';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

import ServiceAccountsListPage from './ServiceAccountsListPage';

const createdAt = { seconds: BigInt(1717000000), nanos: 0 };

const makeServiceAccount = (
  id: string,
  name: string,
  state: ServiceAccountState = ServiceAccountState.READY,
  deleting = false,
): ServiceAccount =>
  create(ServiceAccountSchema, {
    id,
    metadata: {
      name,
      creationTimestamp: createdAt,
      ...(deleting ? { deletionTimestamp: createdAt } : {}),
    },
    status: { state },
  });

const defaultServiceAccounts = [
  makeServiceAccount('sa-1', 'build-bot', ServiceAccountState.READY),
  makeServiceAccount('sa-2', 'deploy-bot', ServiceAccountState.PENDING),
  makeServiceAccount('sa-3', 'audit-bot', ServiceAccountState.FAILED),
  makeServiceAccount('sa-4', 'retiring-bot', ServiceAccountState.READY, true),
];

const renderPage = (
  serviceAccounts: ServiceAccount[] = defaultServiceAccounts,
  routerEntries?: string[],
) =>
  renderWithProviders(<ServiceAccountsListPage />, {
    transport: createMockConnectTransport({ serviceAccounts }),
    routerEntries,
  });

describe('ServiceAccountsListPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders service accounts as cards by default', async () => {
    const { container } = renderPage();

    expect(await screen.findByText('build-bot')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    expect(container.querySelectorAll('time')).toHaveLength(defaultServiceAccounts.length);
  });

  it('renders table rows with lifecycle and deletion states and creation timestamps', async () => {
    localStorage.setItem(getViewTypePrefKey('service-accounts'), 'list');
    const { container } = renderPage();

    expect(
      await screen.findByRole('heading', { name: 'Service accounts', level: 1 }),
    ).toBeInTheDocument();
    expect(await screen.findByText('build-bot')).toBeInTheDocument();
    expect(screen.getByText('Create non-human accounts for CI systems and other automation.')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Search service accounts' })).toBeInTheDocument();
    expect(screen.getByText('build-bot')).toBeInTheDocument();
    expect(screen.getByText('deploy-bot')).toBeInTheDocument();
    expect(screen.getByText('audit-bot')).toBeInTheDocument();
    expect(screen.getByText('retiring-bot')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Pending')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('Deleting')).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'Status',
      'Created',
    ]);

    const timestamps = Array.from(container.querySelectorAll('time'));
    expect(timestamps).toHaveLength(defaultServiceAccounts.length);
    expect(timestamps.map((timestamp) => timestamp.dateTime)).toEqual(
      defaultServiceAccounts.map((serviceAccount) =>
        new Date(Number(serviceAccount.metadata?.creationTimestamp?.seconds) * 1000).toISOString(),
      ),
    );
    expect(timestamps.every((timestamp) => timestamp.textContent)).toBe(true);
  });

  it('shows the unfiltered empty state when no service accounts are returned', async () => {
    renderPage([]);

    expect(
      await screen.findByRole('heading', { name: 'No service accounts found', level: 2 }),
    ).toBeInTheDocument();
    expect(screen.getByText('No service accounts are available yet.')).toBeInTheDocument();
  });

  it('filters service accounts by name', async () => {
    renderPage(defaultServiceAccounts, ['/?search=build']);

    expect(await screen.findByText('build-bot')).toBeInTheDocument();
    expect(screen.queryByText('deploy-bot')).not.toBeInTheDocument();
    expect(screen.queryByText('audit-bot')).not.toBeInTheDocument();
  });

  it('clears a search from the filtered empty state', async () => {
    const { user } = renderPage(defaultServiceAccounts, ['/?search=missing-account']);

    expect(
      await screen.findByRole('heading', {
        name: 'No service accounts match your filters',
        level: 2,
      }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));

    expect(await screen.findByText('build-bot')).toBeInTheDocument();
  });

  it('shows a loading indicator while the list request is pending', async () => {
    const response = {
      items: defaultServiceAccounts,
      size: defaultServiceAccounts.length,
      total: defaultServiceAccounts.length,
    };
    let resolveResponse: ((value: typeof response) => void) | undefined;
    const pendingResponse = new Promise<typeof response>((resolve) => {
      resolveResponse = resolve;
    });
    const transport = createMockConnectTransport(
      {},
      { onServiceAccountList: () => pendingResponse },
    );

    renderWithProviders(<ServiceAccountsListPage />, { transport });

    expect(await screen.findByRole('progressbar')).toBeInTheDocument();
    await waitFor(() => {
      expect(resolveResponse).toBeDefined();
    });

    await act(async () => {
      resolveResponse?.(response);
      await pendingResponse;
    });
    expect(await screen.findByText('build-bot')).toBeInTheDocument();
  });

  it('shows an error state when the list request fails', async () => {
    const transport = createMockConnectTransport(
      {},
      {
        onServiceAccountList: () => {
          throw new ConnectError('Unable to load service accounts', Code.Unknown);
        },
      },
    );

    renderWithProviders(<ServiceAccountsListPage />, { transport });

    expect(await screen.findByRole('heading', { name: 'An error occurred' })).toBeInTheDocument();
    expect(screen.getByText('Unable to load service accounts')).toBeInTheDocument();
  });
});
