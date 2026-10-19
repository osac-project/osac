import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Protocol, SecurityGroupState, VirtualNetworkState } from '@osac/types';

import { SecurityGroupDetailPage } from './SecurityGroupDetailPage';
import * as networkingApi from '../../api/v1/networking';

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

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/networking/security-groups/sg-1']}>
      <Routes>
        <Route path="/networking/security-groups/:id" element={<SecurityGroupDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );

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
