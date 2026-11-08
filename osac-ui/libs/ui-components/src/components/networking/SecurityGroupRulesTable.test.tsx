import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

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
    },
    {
      $typeName: 'osac.public.v1.SecurityRule',
      protocol: Protocol.ICMP,
      ipv4Cidr: '10.0.0.0/8',
    },
  ];

  it('renders a read-only empty state for ingress', () => {
    render(<SecurityGroupRulesTable rules={[]} direction="ingress" />);

    expect(screen.getByText(/No inbound rules\. Use Edit rules/)).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('renders a read-only empty state for egress', () => {
    render(<SecurityGroupRulesTable rules={[]} direction="egress" />);

    expect(screen.getByText(/No outbound rules\. Use Edit rules/)).toBeInTheDocument();
  });

  it('renders rules table with protocol, port range, and CIDR columns and no actions', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="ingress" />);

    expect(screen.getByText('Protocol')).toBeInTheDocument();
    expect(screen.getByText('Port range')).toBeInTheDocument();
    expect(screen.getByText('Source CIDR')).toBeInTheDocument();
    expect(screen.queryByText('Actions')).not.toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('shows Destination CIDR label for egress direction', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="egress" />);

    expect(screen.getByText('Destination CIDR')).toBeInTheDocument();
  });

  it('formats protocol names correctly', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="ingress" />);

    expect(screen.getByText('TCP')).toBeInTheDocument();
    expect(screen.getByText('UDP')).toBeInTheDocument();
    expect(screen.getByText('ICMP')).toBeInTheDocument();
  });

  it('formats port range as single port when from equals to', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="ingress" />);

    expect(screen.getByText('80')).toBeInTheDocument();
    expect(screen.getByText('53')).toBeInTheDocument();
  });

  it('formats IPv4 CIDRs', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="ingress" />);

    expect(screen.getByText('0.0.0.0/0')).toBeInTheDocument();
    expect(screen.queryByText('::/0')).not.toBeInTheDocument();
    expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument();
  });
});
