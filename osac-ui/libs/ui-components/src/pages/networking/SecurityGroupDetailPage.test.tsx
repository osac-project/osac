import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Protocol, type SecurityGroup, SecurityGroupState, VirtualNetworkState } from '@osac/types';

import {
  SecurityGroupDetailPage,
  securityGroupRuleEditingDisabledReason,
} from './SecurityGroupDetailPage';
import * as networkingApi from '../../api/v1/networking';
import { tIdentity } from '../../test-utils/i18n';

vi.mock('../../api/v1/networking', async (importOriginal) => {
  const actual = await importOriginal<typeof networkingApi>();
  return {
    ...actual,
    useSecurityGroup: vi.fn(),
    useVirtualNetworks: vi.fn(),
    useUpdateSecurityGroup: vi.fn(),
    useDeleteSecurityGroup: vi.fn(),
  };
});

const pageElement = () => (
  <MemoryRouter initialEntries={['/networking/security-groups/sg-1']}>
    <Routes>
      <Route path="/networking/security-groups/:id" element={<SecurityGroupDetailPage />} />
    </Routes>
  </MemoryRouter>
);

const renderPage = () => render(pageElement());

describe('SecurityGroupDetailPage', () => {
  const mockVirtualNetworks = [
    {
      id: 'vn-1',
      metadata: { name: 'vn-prod' },
      spec: { ipv4Cidr: '10.0.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    },
  ];

  const mockSecurityGroup = {
    id: 'sg-1',
    metadata: { name: 'sg-web' },
    spec: {
      virtualNetwork: { id: 'vn-1' },
      ingress: [
        { protocol: Protocol.TCP, portFrom: 80, portTo: 80, ipv4Cidr: '0.0.0.0/0' },
        { protocol: Protocol.TCP, portFrom: 443, portTo: 443, ipv4Cidr: '0.0.0.0/0' },
      ],
      egress: [{ protocol: Protocol.ALL }],
    },
    status: { state: SecurityGroupState.READY },
  };

  const mutate = vi.fn();
  const reset = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();

    vi.mocked(networkingApi.useSecurityGroup).mockReturnValue({
      data: mockSecurityGroup,
      isLoading: false,
      error: null,
    } as ReturnType<typeof networkingApi.useSecurityGroup>);

    vi.mocked(networkingApi.useVirtualNetworks).mockReturnValue({
      data: mockVirtualNetworks,
      isLoading: false,
      error: null,
    } as ReturnType<typeof networkingApi.useVirtualNetworks>);

    vi.mocked(networkingApi.useUpdateSecurityGroup).mockReturnValue({
      mutate,
      mutateAsync: vi.fn(),
      isPending: false,
      error: null,
      reset,
    } as unknown as ReturnType<typeof networkingApi.useUpdateSecurityGroup>);

    vi.mocked(networkingApi.useDeleteSecurityGroup).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      error: null,
      reset: vi.fn(),
    } as unknown as ReturnType<typeof networkingApi.useDeleteSecurityGroup>);
  });

  it('renders breadcrumb with link to list page', () => {
    renderPage();

    expect(screen.getByRole('button', { name: /Security groups/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'sg-web' })).toBeInTheDocument();
  });

  it('renders three tabs: Inbound Rules, Outbound Rules, Details', () => {
    renderPage();

    expect(screen.getByRole('tab', { name: /Inbound Rules/i })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /Outbound Rules/i })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /Details/i })).toBeInTheDocument();
  });

  it('displays inbound rules in SecurityGroupRulesTable on Inbound Rules tab', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));

    const tabPanel = within(screen.getByRole('tabpanel'));
    expect(tabPanel.getByText('Protocol')).toBeInTheDocument();
    expect(tabPanel.getAllByText('TCP')).toHaveLength(2);
    expect(tabPanel.getByText('80')).toBeInTheDocument();
    expect(tabPanel.getByText('443')).toBeInTheDocument();
  });

  it('shows FAILED alert when status is FAILED', () => {
    vi.mocked(networkingApi.useSecurityGroup).mockReturnValue({
      data: {
        ...mockSecurityGroup,
        status: { state: SecurityGroupState.FAILED, message: 'Network error' },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof networkingApi.useSecurityGroup>);

    renderPage();

    const alertTitle = screen.getByText('Provisioning failed');
    expect(alertTitle).toBeInTheDocument();
    const alert = alertTitle.closest('.pf-v6-c-alert') as HTMLElement;
    expect(within(alert).getByText('Network error')).toBeInTheDocument();
  });

  it('disables rule editing for an automatic security group and keeps rules visible', async () => {
    const user = userEvent.setup();
    vi.mocked(networkingApi.useSecurityGroup).mockReturnValue({
      data: {
        ...mockSecurityGroup,
        metadata: {
          name: 'sg-web',
          labels: { 'osac.openshift.io/auto-created': 'true' },
        },
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof networkingApi.useSecurityGroup>);
    renderPage();

    await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));
    let tabPanel = within(screen.getByRole('tabpanel'));
    const reason = 'Automatic security group rules cannot be edited.';
    expect(tabPanel.getByText(reason)).toBeInTheDocument();
    expect(tabPanel.getAllByText('TCP')).toHaveLength(2);

    const add = tabPanel.getByRole('button', { name: /Add rule/i });
    expect(add).toHaveAttribute('aria-disabled', 'true');
    await user.click(add);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    const edit = tabPanel.getAllByRole('button', { name: /Edit/i })[0];
    expect(edit).toHaveAttribute('aria-disabled', 'true');
    await user.click(edit);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    const remove = tabPanel.getAllByRole('button', { name: /Delete/i })[0];
    expect(remove).toHaveAttribute('aria-disabled', 'true');
    await user.click(remove);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: /Outbound Rules/i }));
    tabPanel = within(screen.getByRole('tabpanel'));
    expect(tabPanel.getByText(reason)).toBeInTheDocument();
    expect(tabPanel.getByRole('button', { name: /Add rule/i })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('keeps a default security group editable when only the default label is set', async () => {
    const user = userEvent.setup();
    vi.mocked(networkingApi.useSecurityGroup).mockReturnValue({
      data: {
        ...mockSecurityGroup,
        metadata: {
          name: 'sg-web',
          labels: { 'osac.openshift.io/default': 'true' },
        },
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof networkingApi.useSecurityGroup>);
    renderPage();

    await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));
    const tabPanel = within(screen.getByRole('tabpanel'));
    const add = tabPanel.getByRole('button', { name: /Add rule/i });
    expect(add).not.toHaveAttribute('aria-disabled');
    await user.click(add);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('keeps FAILED security groups editable for recovery', async () => {
    vi.mocked(networkingApi.useSecurityGroup).mockReturnValue({
      data: {
        ...mockSecurityGroup,
        status: { state: SecurityGroupState.FAILED, message: 'Network error' },
      },
      isLoading: false,
      error: null,
    } as ReturnType<typeof networkingApi.useSecurityGroup>);
    renderPage();

    await userEvent.setup().click(screen.getByRole('tab', { name: /Inbound Rules/i }));
    const tabPanel = within(screen.getByRole('tabpanel'));
    expect(tabPanel.getByRole('button', { name: /Add rule/i })).not.toHaveAttribute(
      'aria-disabled',
    );
    expect(
      tabPanel.queryByText('Rules cannot be edited while the security group is not ready.'),
    ).not.toBeInTheDocument();
  });

  it('disables rule editing while a rule mutation is pending', async () => {
    const user = userEvent.setup();
    vi.mocked(networkingApi.useUpdateSecurityGroup).mockReturnValue({
      mutate: vi.fn(),
      mutateAsync: vi.fn(),
      isPending: true,
      error: null,
      reset: vi.fn(),
    } as unknown as ReturnType<typeof networkingApi.useUpdateSecurityGroup>);
    renderPage();

    await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));
    const tabPanel = within(screen.getByRole('tabpanel'));
    expect(
      tabPanel.getByText('Rules cannot be edited while a rule change is in progress.'),
    ).toBeInTheDocument();
    expect(tabPanel.getByRole('button', { name: /Add rule/i })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('closes a stale rule dialog when the group becomes unavailable', async () => {
    const user = userEvent.setup();
    const view = renderPage();
    await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));
    const tabPanel = within(screen.getByRole('tabpanel'));
    await user.click(tabPanel.getByRole('button', { name: /Add rule/i }));
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    vi.mocked(networkingApi.useSecurityGroup).mockReturnValue({
      data: { ...mockSecurityGroup, status: { state: SecurityGroupState.PENDING } },
      isLoading: false,
      error: null,
    } as ReturnType<typeof networkingApi.useSecurityGroup>);
    view.rerender(pageElement());

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(
      within(screen.getByRole('tabpanel')).getByText(
        'Rules cannot be edited while the security group is being provisioned or deleted.',
      ),
    ).toBeInTheDocument();
  });

  describe('securityGroupRuleEditingDisabledReason', () => {
    const automaticReason = 'Automatic security group rules cannot be edited.';
    const provisioningReason =
      'Rules cannot be edited while the security group is being provisioned or deleted.';
    const pendingMutationReason = 'Rules cannot be edited while a rule change is in progress.';
    const updatingReason = 'Rules cannot be edited while the security group is updating.';
    const notReadyReason = 'Rules cannot be edited while the security group is not ready.';

    const group = (overrides: Record<string, unknown>): SecurityGroup =>
      ({ ...mockSecurityGroup, ...overrides }) as unknown as SecurityGroup;

    const cases: Array<[string, SecurityGroup, boolean, string | undefined]> = [
      ['READY permits editing', group({}), false, undefined],
      [
        'FAILED permits editing',
        group({ status: { state: SecurityGroupState.FAILED } }),
        false,
        undefined,
      ],
      [
        'default label alone permits editing',
        group({
          metadata: { name: 'sg-web', labels: { 'osac.openshift.io/default': 'true' } },
        }),
        false,
        undefined,
      ],
      [
        'READY with an unrelated message permits editing',
        group({ status: { state: SecurityGroupState.READY, message: 'Rules synced' } }),
        false,
        undefined,
      ],
      [
        'READY with the Updating prefix blocks editing',
        group({ status: { state: SecurityGroupState.READY, message: 'Updating firewall rules' } }),
        false,
        updatingReason,
      ],
      [
        'PENDING blocks editing',
        group({ status: { state: SecurityGroupState.PENDING } }),
        false,
        provisioningReason,
      ],
      [
        'DELETING blocks editing',
        group({ status: { state: SecurityGroupState.DELETING } }),
        false,
        provisioningReason,
      ],
      [
        'UNSPECIFIED blocks editing',
        group({ status: { state: SecurityGroupState.UNSPECIFIED } }),
        false,
        notReadyReason,
      ],
      ['unknown state blocks editing', group({ status: { state: 99 } }), false, notReadyReason],
      [
        'automatic label blocks editing',
        group({
          metadata: {
            name: 'sg-web',
            labels: { 'osac.openshift.io/auto-created': 'true' },
          },
        }),
        false,
        automaticReason,
      ],
      [
        'auto-sg name prefix blocks editing',
        group({ metadata: { name: 'auto-sg-web' } }),
        false,
        automaticReason,
      ],
      [
        'automatic marker takes precedence over FAILED',
        group({
          metadata: {
            name: 'sg-web',
            labels: { 'osac.openshift.io/auto-created': 'true' },
          },
          status: { state: SecurityGroupState.FAILED },
        }),
        false,
        automaticReason,
      ],
      ['a pending mutation blocks editing', group({}), true, pendingMutationReason],
      [
        'PENDING takes precedence over a pending mutation',
        group({ status: { state: SecurityGroupState.PENDING } }),
        true,
        provisioningReason,
      ],
    ];

    it.each(cases)('%s', (_label, securityGroup, pending, expected) => {
      expect(securityGroupRuleEditingDisabledReason(securityGroup, pending, tIdentity)).toBe(
        expected,
      );
    });
  });

  describe('rule deletion', () => {
    it('removes the targeted ingress rule and keeps the rest', async () => {
      const user = userEvent.setup();
      renderPage();
      await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));

      const tabPanel = within(screen.getByRole('tabpanel'));
      const deleteButtons = tabPanel.getAllByRole('button', { name: /^Delete$/i });
      await user.click(deleteButtons[0]);

      const dialog = within(screen.getByRole('dialog'));
      await user.click(dialog.getByRole('button', { name: /^Delete$/i }));

      expect(mutate).toHaveBeenCalledWith(
        {
          object: {
            id: 'sg-1',
            metadata: { name: 'sg-web' },
            spec: {
              virtualNetwork: { id: 'vn-1' },
              ingress: [
                { protocol: Protocol.TCP, portFrom: 443, portTo: 443, ipv4Cidr: '0.0.0.0/0' },
              ],
              egress: [{ protocol: Protocol.ALL }],
            },
          },
        },
        { onSuccess: expect.any(Function) as unknown },
      );
    });

    it('removes the targeted egress rule', async () => {
      const user = userEvent.setup();
      renderPage();

      await user.click(screen.getByRole('tab', { name: /Outbound Rules/i }));

      const tabPanel = within(screen.getByRole('tabpanel'));
      const deleteButtons = tabPanel.getAllByRole('button', { name: /^Delete$/i });
      await user.click(deleteButtons[0]);

      const dialog = within(screen.getByRole('dialog'));
      await user.click(dialog.getByRole('button', { name: /^Delete$/i }));

      expect(mutate).toHaveBeenCalledWith(
        {
          object: {
            id: 'sg-1',
            metadata: { name: 'sg-web' },
            spec: {
              virtualNetwork: { id: 'vn-1' },
              ingress: [
                { protocol: Protocol.TCP, portFrom: 80, portTo: 80, ipv4Cidr: '0.0.0.0/0' },
                { protocol: Protocol.TCP, portFrom: 443, portTo: 443, ipv4Cidr: '0.0.0.0/0' },
              ],
              egress: [],
            },
          },
        },
        { onSuccess: expect.any(Function) as unknown },
      );
    });

    it('closes the delete rule modal on Cancel', async () => {
      const user = userEvent.setup();
      renderPage();
      await user.click(screen.getByRole('tab', { name: /Inbound Rules/i }));

      const tabPanel = within(screen.getByRole('tabpanel'));
      const deleteButtons = tabPanel.getAllByRole('button', { name: /^Delete$/i });
      await user.click(deleteButtons[0]);

      expect(screen.getByRole('dialog')).toBeInTheDocument();

      const dialog = within(screen.getByRole('dialog'));
      await user.click(dialog.getByRole('button', { name: /Cancel/i }));

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(mutate).not.toHaveBeenCalled();
    });
  });
});
