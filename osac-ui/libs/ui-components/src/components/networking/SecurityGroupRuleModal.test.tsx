import { type MessageInitShape, create } from '@bufbuild/protobuf';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Protocol, type SecurityGroup, SecurityGroupSchema } from '@osac/types';

import { SecurityGroupRuleModal } from './SecurityGroupRuleModal';
import * as networkingApi from '../../api/v1/networking';

vi.mock('../../api/v1/networking', async (importOriginal) => {
  const actual = await importOriginal<typeof networkingApi>();
  return {
    ...actual,
    useUpdateSecurityGroup: vi.fn(),
  };
});

describe('SecurityGroupRuleModal', () => {
  const mutateAsync =
    vi.fn<
      (request: { object: MessageInitShape<typeof SecurityGroupSchema> }) => Promise<unknown>
    >();

  const securityGroup: SecurityGroup = {
    id: 'sg-1',
    metadata: { name: 'sg-web' },
    spec: {
      virtualNetwork: 'vn-1',
      ingress: [{ protocol: Protocol.TCP, portFrom: 80, portTo: 80, ipv4Cidr: '0.0.0.0/0' }],
      egress: [],
    },
  } as unknown as SecurityGroup;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(networkingApi.useUpdateSecurityGroup).mockReturnValue({
      mutateAsync,
      isPending: false,
      error: null,
    } as unknown as ReturnType<typeof networkingApi.useUpdateSecurityGroup>);
  });

  it('renders "Add rule" title in add mode', () => {
    render(
      <SecurityGroupRuleModal
        onClose={vi.fn()}
        securityGroup={securityGroup}
        direction="ingress"
      />,
    );

    expect(screen.getByText('Add rule')).toBeInTheDocument();
  });

  it('renders "Edit rule" title with pre-filled values in edit mode', () => {
    render(
      <SecurityGroupRuleModal
        onClose={vi.fn()}
        securityGroup={securityGroup}
        direction="ingress"
        ruleIndex={0}
      />,
    );

    expect(screen.getByText('Edit rule')).toBeInTheDocument();
    expect(screen.getByLabelText(/Protocol/i)).toHaveTextContent('TCP');
    expect(screen.getByLabelText(/Port From/i)).toHaveValue(80);
  });

  it('adds a new rule by pushing it onto the ingress array', async () => {
    const user = userEvent.setup();
    mutateAsync.mockResolvedValue({ id: 'sg-1' });
    const onClose = vi.fn();

    render(
      <SecurityGroupRuleModal onClose={onClose} securityGroup={securityGroup} direction="egress" />,
    );

    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: 'All' }));
    await user.click(screen.getByLabelText(/IPv4 CIDR$/i));
    await user.type(screen.getByLabelText(/IPv4 CIDR$/i), '10.0.0.0/24');
    await user.click(screen.getByRole('button', { name: /Add/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledTimes(1);
    });
    expect(mutateAsync.mock.calls[0][0]).toMatchObject({
      object: {
        id: 'sg-1',
        spec: {
          egress: [{ protocol: Protocol.ALL, ipv4Cidr: '10.0.0.0/24' }],
        },
      },
    });
    expect(onClose).toHaveBeenCalled();
  });

  it('edits a rule by replacing it at ruleIndex', async () => {
    const user = userEvent.setup();
    mutateAsync.mockResolvedValue({ id: 'sg-1' });
    const onClose = vi.fn();

    render(
      <SecurityGroupRuleModal
        onClose={onClose}
        securityGroup={securityGroup}
        direction="ingress"
        ruleIndex={0}
      />,
    );

    const portFromInput = screen.getByLabelText(/Port From/i);
    await user.clear(portFromInput);
    await user.type(portFromInput, '443');
    const portToInput = screen.getByLabelText(/Port To/i);
    await user.clear(portToInput);
    await user.type(portToInput, '443');
    await user.click(screen.getByRole('button', { name: /Save/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledTimes(1);
    });
    expect(mutateAsync.mock.calls[0][0]).toMatchObject({
      object: {
        id: 'sg-1',
        spec: {
          ingress: [{ portFrom: 443, portTo: 443 }],
        },
      },
    });
    expect(onClose).toHaveBeenCalled();
  });

  it.each([
    { direction: 'ingress' as const, protocol: Protocol.TCP, label: 'TCP' },
    { direction: 'ingress' as const, protocol: Protocol.UDP, label: 'UDP' },
    { direction: 'egress' as const, protocol: Protocol.TCP, label: 'TCP' },
    { direction: 'egress' as const, protocol: Protocol.UDP, label: 'UDP' },
  ])('omits ports when editing $direction from $label to All', async ({ direction, protocol }) => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const beforeRule = { protocol: Protocol.TCP, portFrom: 22, portTo: 22, ipv4Cidr: '10.0.0.0/8' };
    const editedRule = { protocol, portFrom: 8000, portTo: 9000, ipv4Cidr: '192.168.0.0/16' };
    const afterRule = { protocol: Protocol.UDP, portFrom: 53, portTo: 53, ipv4Cidr: '0.0.0.0/0' };
    const oppositeDirection = direction === 'ingress' ? 'egress' : 'ingress';
    const spec = {
      virtualNetwork: { id: 'vn-1' },
      [direction]: [beforeRule, editedRule, afterRule],
      [oppositeDirection]: [beforeRule, afterRule],
    };
    const group = create(SecurityGroupSchema, { id: 'sg-1', metadata: { name: 'sg-web' }, spec });
    const originalGroup = structuredClone(group);
    mutateAsync.mockResolvedValue({ object: group });

    render(
      <SecurityGroupRuleModal
        onClose={onClose}
        securityGroup={group}
        direction={direction}
        ruleIndex={1}
      />,
    );

    expect(screen.getByLabelText(/Port From/i)).toHaveValue(8000);
    expect(screen.getByLabelText(/Port To/i)).toHaveValue(9000);
    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: 'All' }));
    expect(screen.queryByLabelText(/Port From/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Port To/i)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    const submitted = mutateAsync.mock.calls[0][0].object;
    expect(submitted.spec?.[direction]?.[1]).not.toHaveProperty('portFrom');
    expect(submitted.spec?.[direction]?.[1]).not.toHaveProperty('portTo');
    expect(submitted).toEqual({
      id: group.id,
      metadata: { name: 'sg-web' },
      spec: {
        ...spec,
        virtualNetwork: group.spec?.virtualNetwork,
        [direction]: [
          beforeRule,
          { protocol: Protocol.ALL, ipv4Cidr: editedRule.ipv4Cidr },
          afterRule,
        ],
      },
    });
    expect(group).toEqual(originalGroup);
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it('omits entered ports when adding a TCP rule changed to All', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    mutateAsync.mockResolvedValue({ id: 'sg-1' });

    render(
      <SecurityGroupRuleModal onClose={onClose} securityGroup={securityGroup} direction="egress" />,
    );

    await user.type(screen.getByLabelText(/Port From/i), '8000');
    await user.type(screen.getByLabelText(/Port To/i), '9000');
    await user.type(screen.getByLabelText(/IPv4 CIDR$/i), '10.0.0.0/8');
    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: 'All' }));
    expect(screen.queryByLabelText(/Port From/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Port To/i)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0][0].object.spec).toEqual({
      virtualNetwork: securityGroup.spec?.virtualNetwork,
      ingress: securityGroup.spec?.ingress,
      egress: [{ protocol: Protocol.ALL, ipv4Cidr: '10.0.0.0/8' }],
    });
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it.each([
    { protocol: Protocol.TCP, label: 'TCP' },
    { protocol: Protocol.UDP, label: 'UDP' },
  ])('submits entered ports when editing All to $label', async ({ protocol, label }) => {
    const user = userEvent.setup();
    const group = create(SecurityGroupSchema, {
      id: 'sg-1',
      spec: { ingress: [{ protocol: Protocol.ALL, ipv4Cidr: '10.0.0.0/8' }] },
    });
    mutateAsync.mockResolvedValue({ object: group });

    render(
      <SecurityGroupRuleModal
        onClose={vi.fn()}
        securityGroup={group}
        direction="ingress"
        ruleIndex={0}
      />,
    );

    expect(screen.queryByLabelText(/Port From/i)).not.toBeInTheDocument();
    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: label }));
    await user.type(screen.getByLabelText(/Port From/i), '8000');
    await user.type(screen.getByLabelText(/Port To/i), '9000');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0][0].object.spec?.ingress).toEqual([
      { protocol, portFrom: 8000, portTo: 9000, ipv4Cidr: '10.0.0.0/8' },
    ]);
  });

  it('rejects semantically invalid IPv4 CIDR 999.555.666.777/99 (OSAC-3899)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    render(
      <SecurityGroupRuleModal onClose={onClose} securityGroup={securityGroup} direction="egress" />,
    );

    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: 'All' }));
    await user.click(screen.getByLabelText(/IPv4 CIDR$/i));
    await user.type(screen.getByLabelText(/IPv4 CIDR$/i), '999.555.666.777/99');
    await user.tab();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Add/i })).toBeDisabled();
    });
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('rejects IPv4 CIDR with out-of-range prefix 192.168.1.0/55 (OSAC-3899)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    render(
      <SecurityGroupRuleModal onClose={onClose} securityGroup={securityGroup} direction="egress" />,
    );

    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: 'All' }));
    await user.click(screen.getByLabelText(/IPv4 CIDR$/i));
    await user.type(screen.getByLabelText(/IPv4 CIDR$/i), '192.168.1.0/55');
    await user.tab();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Add/i })).toBeDisabled();
    });
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('does not offer ICMP for a new rule', async () => {
    const user = userEvent.setup();

    render(
      <SecurityGroupRuleModal onClose={vi.fn()} securityGroup={securityGroup} direction="egress" />,
    );

    await user.click(screen.getByLabelText(/^Protocol/i));

    expect(screen.getByRole('option', { name: 'All' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'ICMP' })).not.toBeInTheDocument();
  });

  it('keeps ICMP when editing a stored ICMP rule', () => {
    const withIcmp = {
      ...securityGroup,
      spec: {
        ...securityGroup.spec,
        ingress: [{ protocol: Protocol.ICMP, ipv4Cidr: '10.0.0.0/8' }],
      },
    } as unknown as SecurityGroup;

    render(
      <SecurityGroupRuleModal
        onClose={vi.fn()}
        securityGroup={withIcmp}
        direction="ingress"
        ruleIndex={0}
      />,
    );

    expect(screen.getByLabelText(/Protocol/i)).toHaveTextContent('ICMP');
  });
});
