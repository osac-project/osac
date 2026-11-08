import { Route, Routes } from 'react-router-dom';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  SecurityGroup,
  SecurityGroupsUpdateRequest,
  SecurityGroupsUpdateResponse,
} from '@osac/types';
import { Protocol, SecurityGroupState } from '@osac/types';

import { SecurityGroupEditRulesPage } from './SecurityGroupEditRulesPage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const securityGroup = {
  id: 'sg-1',
  metadata: { name: 'sg-web' },
  spec: {
    virtualNetwork: { id: 'vn-1' },
    ingress: [
      { protocol: Protocol.TCP, portFrom: 80, portTo: 80, ipv4Cidr: '0.0.0.0/0' },
      { protocol: Protocol.ICMP, ipv4Cidr: '10.0.0.0/8' },
    ],
    egress: [{ protocol: Protocol.ALL, ipv4Cidr: '0.0.0.0/0' }],
  },
  status: { state: SecurityGroupState.READY },
} as SecurityGroup;

const renderPage = (
  overrides: Partial<SecurityGroup> = {},
  transportOverrides: MockTransportOverrides = {},
) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/security-groups/:id" element={<h1>Security group details</h1>} />
      <Route
        path="/networking/security-groups/:id/edit-rules"
        element={<SecurityGroupEditRulesPage />}
      />
    </Routes>,
    {
      routerEntries: ['/networking/security-groups/sg-1/edit-rules'],
      apiFixtures: { securityGroups: [{ ...securityGroup, ...overrides } as SecurityGroup] },
      transportOverrides,
      session: { role: 'tenant-user' },
    },
  );

const captureUpdates = () => {
  const update = vi.fn();
  const transportOverrides: MockTransportOverrides = {
    onSecurityGroupUpdate: (req: SecurityGroupsUpdateRequest) => {
      update(req);
      return { object: req.object } as SecurityGroupsUpdateResponse;
    },
  };
  return { update, transportOverrides };
};

const inboundSection = () => within(screen.getByRole('group', { name: 'Inbound rules' }));
const outboundSection = () => within(screen.getByRole('group', { name: 'Outbound rules' }));

const plainRules = (req: SecurityGroupsUpdateRequest, direction: 'ingress' | 'egress') =>
  (req.object?.spec?.[direction] ?? []).map(({ protocol, portFrom, portTo, ipv4Cidr }) => ({
    protocol,
    portFrom,
    portTo,
    ipv4Cidr,
  }));

describe('SecurityGroupEditRulesPage', () => {
  it('loads one row per rule', async () => {
    renderPage();

    expect(await screen.findByRole('group', { name: 'Inbound rules' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Edit rules' })).toBeInTheDocument();
    expect(inboundSection().getAllByLabelText(/^Protocol/)).toHaveLength(2);
    expect(outboundSection().getAllByLabelText(/^Protocol/)).toHaveLength(1);
    expect(inboundSection().getByLabelText(/^Port from/)).toHaveValue(80);
    expect(outboundSection().queryByLabelText(/^Port from/)).not.toBeInTheDocument();
  });

  it('adds and removes rules and saves the complete lists in one update', async () => {
    const { update, transportOverrides } = captureUpdates();
    const { user } = renderPage({}, transportOverrides);
    await screen.findByRole('group', { name: 'Inbound rules' });

    await user.click(inboundSection().getByRole('button', { name: 'Add rule' }));
    const portFroms = inboundSection().getAllByLabelText(/^Port from/);
    const portTos = inboundSection().getAllByLabelText(/^Port to/);
    const cidrs = inboundSection().getAllByLabelText(/^Source IPv4 CIDR/);
    await user.type(portFroms[1], '8000');
    await user.type(portTos[1], '9000');
    await user.type(cidrs[2], '192.168.0.0/16');
    await user.click(outboundSection().getByRole('button', { name: 'Remove rule 1' }));

    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(
      await screen.findByRole('heading', { name: 'Security group details' }),
    ).toBeInTheDocument();
    expect(update).toHaveBeenCalledTimes(1);
    const req = update.mock.calls[0][0] as SecurityGroupsUpdateRequest;
    expect(req.object?.id).toBe('sg-1');
    expect(req.object?.spec?.virtualNetwork?.id).toBe('vn-1');
    expect(plainRules(req, 'ingress')).toEqual([
      { protocol: Protocol.TCP, portFrom: 80, portTo: 80, ipv4Cidr: '0.0.0.0/0' },
      { protocol: Protocol.ICMP, portFrom: undefined, portTo: undefined, ipv4Cidr: '10.0.0.0/8' },
      { protocol: Protocol.TCP, portFrom: 8000, portTo: 9000, ipv4Cidr: '192.168.0.0/16' },
    ]);
    expect(plainRules(req, 'egress')).toEqual([]);
  }, 15000);

  it('drops the ports of a rule switched to All', async () => {
    const { update, transportOverrides } = captureUpdates();
    const { user } = renderPage({}, transportOverrides);
    await screen.findByRole('group', { name: 'Inbound rules' });

    await user.click(inboundSection().getAllByLabelText(/^Protocol/)[0]);
    await user.click(screen.getByRole('option', { name: 'All' }));
    expect(inboundSection().queryByLabelText(/^Port from/)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
    expect(
      plainRules(update.mock.calls[0][0] as SecurityGroupsUpdateRequest, 'ingress')[0],
    ).toEqual({
      protocol: Protocol.ALL,
      portFrom: undefined,
      portTo: undefined,
      ipv4Cidr: '0.0.0.0/0',
    });
  });

  it('validates on Save and does not send an invalid rule', async () => {
    const { update, transportOverrides } = captureUpdates();
    const { user } = renderPage({}, transportOverrides);
    await screen.findByRole('group', { name: 'Inbound rules' });

    await user.click(inboundSection().getByRole('button', { name: 'Add rule' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByText('Port from is required for TCP and UDP')).toBeInTheDocument();
    expect(screen.getByText('Port to is required for TCP and UDP')).toBeInTheDocument();
    expect(screen.getByText('IPv4 CIDR is required')).toBeInTheDocument();
    expect(update).not.toHaveBeenCalled();
  });

  it.each(['999.555.666.777/99', '192.168.1.0/55'])(
    'rejects the invalid IPv4 CIDR %s on blur',
    async (cidr) => {
      const { user } = renderPage();
      await screen.findByRole('group', { name: 'Inbound rules' });

      const field = inboundSection().getAllByLabelText(/^Source IPv4 CIDR/)[0];
      await user.clear(field);
      await user.type(field, cidr);
      await user.tab();

      expect(await screen.findByText('Invalid IPv4 CIDR notation')).toBeInTheDocument();
    },
  );

  it('rejects a port range that ends before it starts', async () => {
    const { user } = renderPage();
    await screen.findByRole('group', { name: 'Inbound rules' });

    const portTo = inboundSection().getByLabelText(/^Port to/);
    await user.clear(portTo);
    await user.type(portTo, '79');
    await user.tab();

    expect(
      await screen.findByText('Port to must be greater than or equal to Port from'),
    ).toBeInTheDocument();
  });

  it('offers ICMP only on a stored ICMP rule', async () => {
    const { user } = renderPage();
    await screen.findByRole('group', { name: 'Inbound rules' });

    const protocols = inboundSection().getAllByLabelText(/^Protocol/);
    await user.click(protocols[1]);
    expect(screen.getByRole('option', { name: 'ICMP' })).toBeInTheDocument();
    await user.keyboard('{Escape}');

    await user.click(protocols[0]);
    expect(screen.queryByRole('option', { name: 'ICMP' })).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'TCP' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'UDP' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'All' })).toBeInTheDocument();
  });

  it.each([
    ['PENDING', { state: SecurityGroupState.PENDING }],
    ['READY with the Updating message', { state: SecurityGroupState.READY, message: 'Updating' }],
  ])('blocks saving while %s', async (_name, status) => {
    renderPage({ status } as never);

    expect(await screen.findByText('Rules cannot be edited now')).toBeInTheDocument();
    expect(
      screen.getByText('Available when the security group is Ready or Failed.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(inboundSection().getByRole('button', { name: 'Add rule' })).toBeDisabled();
  });

  it('blocks saving on an automatic group', async () => {
    renderPage({
      metadata: { name: 'auto-sg', labels: { 'osac.openshift.io/auto-created': 'true' } },
    } as never);

    expect(
      await screen.findByText('The rules of an automatic security group cannot be edited.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  });

  it('keeps the form with the server error', async () => {
    const { user } = renderPage(
      {},
      {
        onSecurityGroupUpdate: () => {
          throw new ConnectError('ICMP is not supported', Code.InvalidArgument);
        },
      },
    );
    await screen.findByRole('group', { name: 'Inbound rules' });

    await user.click(screen.getByRole('button', { name: 'Save' }));

    const alertTitle = await screen.findByText('Failed to save rules');
    expect(screen.getByText(/ICMP is not supported/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Edit rules' })).toBeInTheDocument();
    // The message is above the form, so it is seen without scrolling past the rules.
    expect(
      alertTitle.compareDocumentPosition(screen.getByRole('group', { name: 'Inbound rules' })) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it('marks the ICMP rows when the server rejects ICMP until they change', async () => {
    const { user } = renderPage(
      {},
      {
        onSecurityGroupUpdate: () => {
          throw new ConnectError('ICMP is not supported', Code.InvalidArgument);
        },
      },
    );
    await screen.findByRole('group', { name: 'Inbound rules' });

    await user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Failed to save rules');

    const icmpMark =
      'The network does not support ICMP rules. Remove the rule, or use All, which also allows TCP and UDP on every port.';
    expect(await inboundSection().findAllByText(icmpMark)).toHaveLength(1);
    expect(outboundSection().queryByText(icmpMark)).not.toBeInTheDocument();

    const [, icmpProtocol] = inboundSection().getAllByLabelText(/^Protocol/);
    expect(icmpProtocol).toHaveTextContent('ICMP');
    await user.click(icmpProtocol);
    await user.click(screen.getByRole('option', { name: 'All' }));

    await waitFor(() => expect(inboundSection().queryByText(icmpMark)).not.toBeInTheDocument());
  });

  it('returns to the details page on Cancel without saving', async () => {
    const { update, transportOverrides } = captureUpdates();
    const { user } = renderPage({}, transportOverrides);

    await user.click(await screen.findByRole('button', { name: 'Cancel' }));

    expect(
      await screen.findByRole('heading', { name: 'Security group details' }),
    ).toBeInTheDocument();
    expect(update).not.toHaveBeenCalled();
  });
});
