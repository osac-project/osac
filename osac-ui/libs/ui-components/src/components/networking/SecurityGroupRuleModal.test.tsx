import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Protocol, type SecurityGroup } from '@osac/types';

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
  const mutateAsync = vi.fn();

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

  it('rejects invalid IPv6 CIDR 00:000000:000000:00000:00/0 (OSAC-3899)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    render(
      <SecurityGroupRuleModal onClose={onClose} securityGroup={securityGroup} direction="egress" />,
    );

    await user.click(screen.getByLabelText(/^Protocol/i));
    await user.click(screen.getByRole('option', { name: 'All' }));
    await user.click(screen.getByLabelText(/IPv6 CIDR/i));
    await user.type(screen.getByLabelText(/IPv6 CIDR/i), '00:000000:000000:00000:00/0');
    await user.tab();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Add/i })).toBeDisabled();
    });
    expect(mutateAsync).not.toHaveBeenCalled();
  });
});
