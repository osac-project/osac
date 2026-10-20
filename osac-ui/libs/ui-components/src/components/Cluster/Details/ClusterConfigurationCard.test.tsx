import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import { ApiProvider } from '../../../api/api-context';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';

const obsoleteVersion = create(ClusterVersionSchema, {
  id: 'v4.15',
  metadata: { name: 'v4.15' },
  spec: { version: '4.15.0', state: ClusterVersionState.OBSOLETE, enabled: true },
});

const renderCard = (cluster: Cluster) => {
  const transport = createMockConnectTransport({ clusterVersions: [obsoleteVersion] });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return render(<ClusterConfigurationCard cluster={cluster} />, { wrapper });
};

describe('ClusterConfigurationCard version join', () => {
  it('shows a skeleton while loading, then the resolved version and lifecycle label', async () => {
    const { container } = renderCard(
      create(ClusterSchema, { id: 'cl-1', spec: { version: { id: 'v4.15', name: 'v4.15' } } }),
    );

    expect(container.querySelector('.pf-v6-c-skeleton')).not.toBeNull();

    // Get resolves regardless of lifecycle state (obsolete included).
    expect(await screen.findByText('4.15.0')).toBeInTheDocument();
    const versionGroup = screen.getByText('Version').closest('.pf-v6-c-description-list__group');
    expect(versionGroup).toHaveTextContent('Version');
    expect(versionGroup).toHaveTextContent('4.15.0');
    expect(versionGroup).toHaveTextContent('Obsolete');
  });

  it('falls back to the raw reference name with no label when unresolved', async () => {
    renderCard(
      create(ClusterSchema, {
        id: 'cl-2',
        spec: { version: { id: 'gone', name: '4.99.0-missing' } },
      }),
    );

    expect(await screen.findByText('4.99.0-missing')).toBeInTheDocument();
    const versionGroup = screen
      .getByText('4.99.0-missing')
      .closest('.pf-v6-c-description-list__group');
    expect(versionGroup).toHaveTextContent('Version');
    expect(versionGroup?.querySelector('.pf-v6-c-label')).toBeNull();
  });

  it('falls back to the raw reference name with no label when the version id is empty (legacy cluster)', async () => {
    renderCard(
      create(ClusterSchema, {
        id: 'cl-3',
        spec: { version: { id: '', name: '4.14.0-legacy' } },
      }),
    );

    // Empty id disables the Get query; no skeleton, straight to the raw name.
    expect(await screen.findByText('4.14.0-legacy')).toBeInTheDocument();
    const versionGroup = screen
      .getByText('4.14.0-legacy')
      .closest('.pf-v6-c-description-list__group');
    expect(versionGroup).toHaveTextContent('Version');
    expect(versionGroup?.querySelector('.pf-v6-c-label')).toBeNull();
  });
});
