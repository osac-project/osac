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
    expect(dashes.length).toBeGreaterThanOrEqual(3);
  });

  it('shows spinner and Pending for ingress endpoint while provisioning', () => {
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
        ingressEndpoint: '',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('Pending')).toBeInTheDocument();
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows resolved ingress endpoint when ready', () => {
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
        ingressEndpoint: '10.0.0.42',
      },
    });

    render(<ClusterNetworkingCard cluster={cluster} />);

    expect(screen.getByText('10.0.0.42')).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });
});
