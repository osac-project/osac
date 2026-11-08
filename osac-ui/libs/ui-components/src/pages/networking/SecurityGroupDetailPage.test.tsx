import { Route, Routes } from 'react-router-dom';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { BareMetalInstance, SecurityGroup, VirtualNetwork } from '@osac/types';
import { Protocol, SecurityGroupState, VirtualNetworkState } from '@osac/types';

import { SecurityGroupDetailPage } from './SecurityGroupDetailPage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const virtualNetwork = {
  id: 'vn-1',
  metadata: { name: 'vn-prod' },
  spec: { ipv4Cidr: '10.0.0.0/16' },
  status: { state: VirtualNetworkState.READY },
} as VirtualNetwork;

const securityGroup = {
  id: 'sg-1',
  metadata: { name: 'sg-web' },
  spec: {
    virtualNetwork: { id: 'vn-1' },
    ingress: [
      { protocol: Protocol.TCP, portFrom: 80, portTo: 80, ipv4Cidr: '0.0.0.0/0' },
      { protocol: Protocol.TCP, portFrom: 443, portTo: 443, ipv4Cidr: '0.0.0.0/0' },
    ],
    egress: [{ protocol: Protocol.ALL, ipv4Cidr: '0.0.0.0/0' }],
  },
  status: { state: SecurityGroupState.READY },
} as SecurityGroup;

const renderPage = (
  overrides: Partial<SecurityGroup> = {},
  bareMetalInstances: BareMetalInstance[] = [],
  transportOverrides: MockTransportOverrides = {},
) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/security-groups" element={<h1>Security groups list</h1>} />
      <Route path="/networking/security-groups/:id" element={<SecurityGroupDetailPage />} />
      <Route path="/networking/security-groups/:id/edit-rules" element={<h1>Edit rules page</h1>} />
    </Routes>,
    {
      routerEntries: ['/networking/security-groups/sg-1'],
      apiFixtures: {
        virtualNetworks: [virtualNetwork],
        securityGroups: [{ ...securityGroup, ...overrides } as SecurityGroup],
        bareMetalInstances,
      },
      transportOverrides,
      session: { role: 'tenant-admin' },
    },
  );

describe('SecurityGroupDetailPage', () => {
  it('renders the breadcrumb, title and status', async () => {
    renderPage();

    expect(await screen.findByRole('heading', { name: 'sg-web' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Security groups' })).toHaveAttribute(
      'href',
      '/networking/security-groups',
    );
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });

  it('renders the Details, Inbound rules and Outbound rules tabs', async () => {
    renderPage();

    expect(await screen.findByRole('tab', { name: 'Details' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Inbound rules' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Outbound rules' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'vn-prod' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
  });

  it('shows the project and the creation time, not the name again', async () => {
    renderPage();

    const details = within(await screen.findByRole('tabpanel', { name: 'Details' }));
    expect(details.getByText('Project')).toBeInTheDocument();
    expect(details.getByText('Root')).toBeInTheDocument();
    expect(details.getByText('Created')).toBeInTheDocument();
    // The title already names the group.
    expect(details.queryByText('Name')).not.toBeInTheDocument();
  });

  it('shows the inbound rules read-only', async () => {
    const { user } = renderPage();
    await user.click(await screen.findByRole('tab', { name: 'Inbound rules' }));

    const tabPanel = within(screen.getByRole('tabpanel'));
    expect(tabPanel.getAllByText('TCP')).toHaveLength(2);
    expect(tabPanel.getByText('80')).toBeInTheDocument();
    expect(tabPanel.getByText('443')).toBeInTheDocument();
    expect(tabPanel.queryByRole('button')).not.toBeInTheDocument();
  });

  it('shows the message of a FAILED group once, in the alert with the next step', async () => {
    renderPage({ status: { state: SecurityGroupState.FAILED, message: 'Network error' } } as never);

    const alertTitle = await screen.findByText('Security group failed');
    const alert = alertTitle.closest('.pf-v6-c-alert') as HTMLElement;
    expect(within(alert).getByText('Network error')).toBeInTheDocument();
    expect(
      within(alert).getByText(
        'The rules below are saved but may not be enforced: each part of the network keeps the rules of its last successful update until a save succeeds.',
      ),
    ).toBeInTheDocument();
    expect(
      within(alert).getByText('Fix the rules with Edit rules and save them again.'),
    ).toBeInTheDocument();
    expect(screen.getAllByText('Network error')).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Failed' })).not.toBeInTheDocument();
  });

  it('shows the alert of a FAILED group without a message', async () => {
    renderPage({ status: { state: SecurityGroupState.FAILED } } as never);

    const alertTitle = await screen.findByText('Security group failed');
    const alert = alertTitle.closest('.pf-v6-c-alert') as HTMLElement;
    expect(within(alert).getByText(/may not be enforced/)).toBeInTheDocument();
    expect(
      within(alert).getByText('Fix the rules with Edit rules and save them again.'),
    ).toBeInTheDocument();
  });

  it('opens the Edit rules page from the header action', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('button', { name: 'Edit rules' }));

    expect(await screen.findByRole('heading', { name: 'Edit rules page' })).toBeInTheDocument();
  });

  it.each([
    ['PENDING', { state: SecurityGroupState.PENDING }],
    ['READY with the Updating message', { state: SecurityGroupState.READY, message: 'Updating' }],
  ])('disables Edit rules with a reason while %s', async (_name, status) => {
    const { user } = renderPage({ status } as never);

    const editRules = await screen.findByRole('button', { name: 'Edit rules' });
    expect(editRules).toHaveAttribute('aria-disabled', 'true');
    await user.click(editRules);
    expect(screen.queryByRole('heading', { name: 'Edit rules page' })).not.toBeInTheDocument();
    editRules.focus();
    expect(
      await screen.findByText('Available when the security group is Ready or Failed.'),
    ).toBeInTheDocument();
  });

  it('keeps Edit rules available on a FAILED group', async () => {
    renderPage({ status: { state: SecurityGroupState.FAILED, message: 'apply failed' } } as never);

    expect(await screen.findByRole('button', { name: 'Edit rules' })).not.toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('shows Updating, not Ready, while a rule update runs', async () => {
    renderPage({ status: { state: SecurityGroupState.READY, message: 'Updating' } } as never);

    expect(await screen.findByText('Updating')).toBeInTheDocument();
    expect(screen.queryByText('Ready')).not.toBeInTheDocument();
  });

  it('shows the status message in a popover', async () => {
    const { user } = renderPage({
      status: { state: SecurityGroupState.READY, message: 'Rules applied' },
    } as never);

    await user.click(await screen.findByRole('button', { name: 'Ready' }));

    expect(await screen.findByText('Status details')).toBeInTheDocument();
    expect(screen.getByText('Rules applied')).toBeInTheDocument();
  });

  it('disables Edit rules and Delete on an automatic group and shows the Auto badge', async () => {
    const { user } = renderPage({
      metadata: {
        name: 'auto-sg-c1',
        labels: {
          'osac.openshift.io/auto-created': 'true',
          'osac.openshift.io/auto-created-for': 'c1',
          'osac.openshift.io/auto-created-kind': 'cluster',
        },
      },
    } as never);

    expect(await screen.findByText('Auto')).toBeInTheDocument();
    const editRules = screen.getByRole('button', { name: 'Edit rules' });
    expect(editRules).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByRole('button', { name: 'Delete security group' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    await user.hover(editRules);
    expect(
      await screen.findByText('Created automatically for cluster c1. It is deleted with it.'),
    ).toBeInTheDocument();
  });

  it('allows Edit rules but disables Delete on the tenant default group', async () => {
    const { user } = renderPage({
      metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
    } as never);

    expect(await screen.findByText('Default')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Edit rules' })).not.toHaveAttribute(
      'aria-disabled',
      'true',
    );
    const deleteButton = screen.getByRole('button', { name: 'Delete security group' });
    expect(deleteButton).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteButton);
    expect(
      await screen.findByText('Created as a tenant default. It is deleted with the tenant.'),
    ).toBeInTheDocument();
  });

  it('deletes the security group after confirmation', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('button', { name: 'Delete security group' }));
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText('Delete sg-web?')).toBeInTheDocument();
    expect(dialog.getByRole('button', { name: 'Delete' })).toHaveClass('pf-m-danger');
  });

  it('disables Delete while a workload uses the group and enables it otherwise', async () => {
    const { user } = renderPage({}, [
      {
        id: 'bm-1',
        metadata: { name: 'gpu-1' },
        spec: { networkAttachments: [{ subnet: { id: 's-1' }, securityGroups: [{ id: 'sg-1' }] }] },
      } as unknown as BareMetalInstance,
    ]);

    const deleteButton = await screen.findByRole('button', { name: 'Delete security group' });
    await waitFor(() => expect(deleteButton).toHaveAttribute('aria-disabled', 'true'));
    await user.hover(deleteButton);
    expect(
      await screen.findByText('Used by bare-metal instance gpu-1. Delete it first.'),
    ).toBeInTheDocument();
  });

  it('enables Delete when no workload uses the group', async () => {
    const onComputeInstanceList = vi.fn();
    renderPage({}, [], { onComputeInstanceList });

    // Check only after the workloads were asked for, not while they load.
    await waitFor(() => expect(onComputeInstanceList).toHaveBeenCalled());
    const deleteButton = await screen.findByRole('button', { name: 'Delete security group' });
    await waitFor(() => expect(deleteButton).not.toHaveAttribute('aria-disabled', 'true'));
  });
});
