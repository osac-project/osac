import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SecurityGroupSchema, SecurityGroupState, SubnetSchema, SubnetState } from '@osac/types';

import DefaultNetworkSummary from './DefaultNetworkSummary';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const makeSubnet = (tenant: string, project: string, cidr: string) =>
  create(SubnetSchema, {
    id: `subnet-${tenant}-${project || 'default'}`,
    metadata: {
      name: 'default-ipv4',
      tenant,
      project,
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: {
      virtualNetwork: { id: `vn-${tenant}`, name: 'default' },
      ipv4Cidr: cidr,
    },
    status: { state: SubnetState.READY },
  });

const makeSecurityGroup = (tenant: string, project: string) =>
  create(SecurityGroupSchema, {
    id: `sg-${tenant}-${project || 'default'}`,
    metadata: {
      name: 'default',
      tenant,
      project,
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: { virtualNetwork: { id: `vn-${tenant}`, name: 'default' } },
    status: { state: SecurityGroupState.READY },
  });

describe('DefaultNetworkSummary', () => {
  it("shows the session tenant's default network", async () => {
    renderWithProviders(<DefaultNetworkSummary tenant="tenant-1" />, {
      session: { role: 'tenant-user', tenantId: 'tenant-1' },
      apiFixtures: {
        subnets: [
          makeSubnet('tenant-1', '', '192.168.100.0/24'),
          makeSubnet('tenant-2', '', '192.168.101.0/24'),
        ],
        securityGroups: [makeSecurityGroup('tenant-1', ''), makeSecurityGroup('tenant-2', '')],
      },
    });

    expect(await screen.findByText('192.168.100.0/24')).toBeInTheDocument();
    expect(screen.queryByText('192.168.101.0/24')).not.toBeInTheDocument();
    expect(screen.queryByText('Tenant default network is unavailable')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Tenant default network details')).toBeInTheDocument();
  });

  it("shows the workload tenant's default network to a cloud provider admin", async () => {
    renderWithProviders(<DefaultNetworkSummary tenant="tenant-2" />, {
      session: { role: 'admin', tenantId: '' },
      apiFixtures: {
        subnets: [
          makeSubnet('tenant-1', '', '192.168.100.0/24'),
          makeSubnet('tenant-2', '', '192.168.101.0/24'),
        ],
        securityGroups: [makeSecurityGroup('tenant-1', ''), makeSecurityGroup('tenant-2', '')],
      },
    });

    expect(await screen.findByText('192.168.101.0/24')).toBeInTheDocument();
    expect(screen.queryByText('192.168.100.0/24')).not.toBeInTheDocument();
  });

  it('ignores a default-labeled subnet outside the default project', async () => {
    renderWithProviders(<DefaultNetworkSummary tenant="tenant-1" />, {
      session: { role: 'tenant-user', tenantId: 'tenant-1' },
      apiFixtures: {
        subnets: [makeSubnet('tenant-1', 'team', '192.168.102.0/24')],
        securityGroups: [makeSecurityGroup('tenant-1', 'team')],
      },
    });

    expect(await screen.findByText('Tenant default network is unavailable')).toBeInTheDocument();
    expect(screen.queryByText('192.168.102.0/24')).not.toBeInTheDocument();
  });

  it('warns when the tenant has no default subnet', async () => {
    renderWithProviders(<DefaultNetworkSummary tenant="tenant-1" />, {
      session: { role: 'tenant-user', tenantId: 'tenant-1' },
      apiFixtures: {
        subnets: [makeSubnet('tenant-2', '', '192.168.101.0/24')],
      },
    });

    expect(await screen.findByText('Tenant default network is unavailable')).toBeInTheDocument();
  });
});
