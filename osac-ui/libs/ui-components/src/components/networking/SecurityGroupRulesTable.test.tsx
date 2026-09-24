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
      ipv6Cidr: '::/0',
    },
    {
      $typeName: 'osac.public.v1.SecurityRule',
      protocol: Protocol.ICMP,
      ipv4Cidr: '10.0.0.0/8',
    },
  ];

  it('renders a read-only empty state for ingress', () => {
    render(<SecurityGroupRulesTable rules={[]} direction="ingress" />);

    expect(screen.getByText('No inbound rules.')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('renders a read-only empty state for egress', () => {
    render(<SecurityGroupRulesTable rules={[]} direction="egress" />);

    expect(screen.getByText('No outbound rules.')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('renders rules without update actions', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="ingress" />);

    expect(screen.getByText('Protocol')).toBeInTheDocument();
    expect(screen.getByText('Port Range')).toBeInTheDocument();
    expect(screen.getByText('Source CIDR')).toBeInTheDocument();
    expect(screen.queryByText('Actions')).not.toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('shows Destination CIDR for egress and formats rule values', () => {
    render(<SecurityGroupRulesTable rules={mockRules} direction="egress" />);

    expect(screen.getByText('Destination CIDR')).toBeInTheDocument();
    expect(screen.getByText('TCP')).toBeInTheDocument();
    expect(screen.getByText('80')).toBeInTheDocument();
    expect(screen.getByText('0.0.0.0/0')).toBeInTheDocument();
    expect(screen.getByText('::/0')).toBeInTheDocument();
  });
});
