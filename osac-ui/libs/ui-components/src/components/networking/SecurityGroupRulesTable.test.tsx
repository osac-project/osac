import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { Protocol, SecurityRule } from '@osac/types';

import { SecurityGroupRulesTable } from './SecurityGroupRulesTable';

describe('SecurityGroupRulesTable', () => {
  const mockRules: SecurityRule[] = [
    {
      $typeName: 'osac.public.v1.SecurityRule',
      protocol: Protocol.TCP,
      portFrom: 80,
      portTo: 80,
      ipv4Cidr: '0.0.0.0/0',
    },
    {
      $typeName: 'osac.public.v1.SecurityRule',
      protocol: Protocol.UDP,
      portFrom: 53,
      portTo: 53,
      ipv6Cidr: '::/0',
    },
    {
      $typeName: 'osac.public.v1.SecurityRule',
      protocol: Protocol.ICMP,
      ipv4Cidr: '10.0.0.0/8',
    },
  ];

  it('renders empty state for ingress with add button', () => {
    const onAddRule = vi.fn();
    render(
      <SecurityGroupRulesTable
        rules={[]}
        direction="ingress"
        onAddRule={onAddRule}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText(/No inbound rules yet/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Add rule/i })).toBeInTheDocument();
  });

  it('renders empty state for egress with add button', () => {
    const onAddRule = vi.fn();
    render(
      <SecurityGroupRulesTable
        rules={[]}
        direction="egress"
        onAddRule={onAddRule}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText(/No outbound rules yet/)).toBeInTheDocument();
  });

  it('renders rules table with protocol, port range, and CIDR columns', () => {
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={vi.fn()}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText('Protocol')).toBeInTheDocument();
    expect(screen.getByText('Port Range')).toBeInTheDocument();
    expect(screen.getByText('Source CIDR')).toBeInTheDocument();
    expect(screen.getByText('Actions')).toBeInTheDocument();
  });

  it('shows Destination CIDR label for egress direction', () => {
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="egress"
        onAddRule={vi.fn()}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText('Destination CIDR')).toBeInTheDocument();
  });

  it('formats protocol names correctly', () => {
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={vi.fn()}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText('TCP')).toBeInTheDocument();
    expect(screen.getByText('UDP')).toBeInTheDocument();
    expect(screen.getByText('ICMP')).toBeInTheDocument();
  });

  it('formats port range as single port when from equals to', () => {
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={vi.fn()}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText('80')).toBeInTheDocument();
    expect(screen.getByText('53')).toBeInTheDocument();
  });

  it('formats IPv4 CIDRs', () => {
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={vi.fn()}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    expect(screen.getByText('0.0.0.0/0')).toBeInTheDocument();
    expect(screen.queryByText('::/0')).not.toBeInTheDocument();
    expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument();
  });

  it('calls onAddRule when Add rule button is clicked', async () => {
    const user = userEvent.setup();
    const onAddRule = vi.fn();
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={onAddRule}
        onEditRule={vi.fn()}
        onDeleteRule={vi.fn()}
      />,
    );

    await user.click(screen.getByRole('button', { name: /Add rule/i }));
    expect(onAddRule).toHaveBeenCalledTimes(1);
  });

  it('calls onEditRule with correct index when Edit is clicked', async () => {
    const user = userEvent.setup();
    const onEditRule = vi.fn();
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={vi.fn()}
        onEditRule={onEditRule}
        onDeleteRule={vi.fn()}
      />,
    );

    const editButtons = screen.getAllByRole('button', { name: /Edit/i });
    await user.click(editButtons[1]);
    expect(onEditRule).toHaveBeenCalledWith(1);
  });

  it('calls onDeleteRule with correct index when Delete is clicked', async () => {
    const user = userEvent.setup();
    const onDeleteRule = vi.fn();
    render(
      <SecurityGroupRulesTable
        rules={mockRules}
        direction="ingress"
        onAddRule={vi.fn()}
        onEditRule={vi.fn()}
        onDeleteRule={onDeleteRule}
      />,
    );

    const deleteButtons = screen.getAllByRole('button', { name: /Delete/i });
    await user.click(deleteButtons[2]);
    expect(onDeleteRule).toHaveBeenCalledWith(2);
  });
});
