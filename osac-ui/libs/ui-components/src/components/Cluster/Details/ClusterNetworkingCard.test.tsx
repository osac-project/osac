import { create } from '@bufbuild/protobuf';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema, ClusterState } from '@osac/types';

import ClusterNetworkingCard from './ClusterNetworkingCard';

describe('ClusterNetworkingCard', () => {
  it('displays the resolved subnet name', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'my-subnet' },
          securityGroups: [],
        },
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('my-subnet')).toBeInTheDocument();
  });

  it('displays a comma-separated list of security group names', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-2',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'subnet-a' },
          securityGroups: [
            { id: 'sg-1', name: 'sg-alpha' },
            { id: 'sg-2', name: 'sg-beta' },
          ],
        },
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('sg-alpha, sg-beta')).toBeInTheDocument();
  });

  it('shows dash for empty networking fields', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-3',
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const dashes = screen.getAllByText('—');
    expect(dashes.length).toBeGreaterThanOrEqual(4);
  });

  it('displays pod CIDR and service CIDR', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-cidr',
      spec: {
        network: {
          podCidr: '10.128.0.0/14',
          serviceCidr: '172.30.0.0/16',
        },
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('10.128.0.0/14')).toBeInTheDocument();
    expect(screen.getByText('172.30.0.0/16')).toBeInTheDocument();
  });

  it('shows "Awaiting provisioning" for endpoints while cluster is progressing', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-4',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'my-subnet' },
          securityGroups: [],
        },
      },
      status: {
        state: ClusterState.PROGRESSING,
        apiEndpoint: '',
        ingressEndpoint: '',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const awaitingElements = screen.getAllByText('Awaiting provisioning');
    expect(awaitingElements).toHaveLength(2);
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows dash for endpoints when cluster is in FAILED state', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-failed',
      status: {
        state: ClusterState.FAILED,
        apiEndpoint: '',
        ingressEndpoint: '',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const dashes = screen.getAllByText('—');
    expect(dashes.length).toBeGreaterThanOrEqual(2);
  });

  it('shows resolved API and ingress endpoints when ready', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-5',
      spec: {
        networkAttachment: {
          subnet: { id: 'subnet-1', name: 'my-subnet' },
          securityGroups: [],
        },
      },
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows "Auto-provisioned" label next to endpoints when autoExternalIpAttachment is true', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-auto',
      spec: {
        autoExternalIpAttachment: true,
      },
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    const labels = screen.getAllByText('Auto-provisioned');
    expect(labels).toHaveLength(2);
    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
  });

  it('does not show "Auto-provisioned" label when autoExternalIpAttachment is false', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-no-auto',
      spec: {
        autoExternalIpAttachment: false,
      },
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.queryByText('Auto-provisioned')).not.toBeInTheDocument();
    expect(screen.getByText('10.0.0.10')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
  });

  it('does not show "Auto-provisioned" label when autoExternalIpAttachment is undefined', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-undef',
      status: {
        state: ClusterState.READY,
        apiEndpoint: '10.0.0.10',
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.queryByText('Auto-provisioned')).not.toBeInTheDocument();
  });
});
