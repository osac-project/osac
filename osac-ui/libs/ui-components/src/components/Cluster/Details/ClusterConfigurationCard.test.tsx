import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ClusterSchema } from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterConfigurationCard', () => {
  it('shows configuration details without repeating either cluster version', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-1',
      metadata: { creator: 'alice' },
      spec: {
        catalogItem: { id: 'catalog-1', name: 'Standard' },
        version: { id: 'v4.18', name: '4.18.0' },
      },
      status: { observedVersion: '4.17.0' },
    });
    const onClusterVersionList = vi.fn(() => ({ items: [] }));

    renderWithProviders(<ClusterConfigurationCard cluster={cluster} />, {
      transportOverrides: { onClusterVersionList },
    });

    expect(screen.getByText('Cluster configuration')).toBeInTheDocument();
    expect(screen.getByText('Standard')).toBeInTheDocument();
    expect(screen.getByText('Creator')).toBeInTheDocument();
    expect(screen.getByText('alice')).toBeInTheDocument();
    expect(screen.queryByText(/version/i)).not.toBeInTheDocument();
    expect(screen.queryByText('4.17.0')).not.toBeInTheDocument();
    expect(screen.queryByText('4.18.0')).not.toBeInTheDocument();
    expect(onClusterVersionList).not.toHaveBeenCalled();
  });
});
