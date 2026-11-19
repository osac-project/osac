import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ClusterSchema, ClusterState } from '@osac/types';

import ClusterDetailsSummary from './ClusterDetailsSummary';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterDetailsSummary', () => {
  it('puts status first, omits worker count and preserves plain-text URLs', () => {
    renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          spec: { nodeSets: { compute: { size: 5 } } },
          status: {
            state: ClusterState.READY,
            apiUrl: 'https://api.example.test:6443',
            consoleUrl: 'https://console.example.test',
          },
        })}
      />,
    );
    expect(screen.getByLabelText('Cluster overview').querySelector('dt')?.textContent).toBe(
      'Status',
    );
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByText('Worker nodes')).not.toBeInTheDocument();
    expect(screen.queryByText('5')).not.toBeInTheDocument();
    expect(screen.getByText('https://api.example.test:6443').closest('a')).toBeNull();
    expect(screen.getByText('https://console.example.test').closest('a')).toBeNull();
  });

  it('links the encoded catalog reference without preflight permissions or resource reads', () => {
    const transport = createMockConnectTransport(
      {},
      {
        onSelfSubjectAccessReview: () => ({ object: { status: { allowed: false } } }),
      },
    );
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          spec: { catalogItem: { id: 'catalog/one ?', name: 'Standard' } },
        })}
      />,
      { transport, apiFixtures: { enabledServices: [] } },
    );
    expect(screen.getByRole('link', { name: 'Standard' })).toHaveAttribute(
      'href',
      '/catalog/cluster/catalog%2Fone%20%3F',
    );
    expect(unary).not.toHaveBeenCalled();
  });

  it.each([undefined, '', ' '])('does not link a missing or blank catalog ID (%s)', (id) => {
    renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          spec: { catalogItem: { id, name: 'Name only' } },
        })}
      />,
    );
    expect(screen.getByText('Name only').closest('a')).toBeNull();
  });

  it.each([
    ['', 'Root'],
    ['team', 'team'],
    ['division.team', 'division.team'],
    [undefined, '—'],
  ])('shows project metadata %s as plain text without lookup or SSAR', (project, label) => {
    const transport = createMockConnectTransport();
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          ...(project === undefined ? {} : { metadata: { project } }),
          spec: { catalogItem: { id: 'catalog-1', name: 'Standard' } },
          status: {
            apiUrl: 'https://api.example.test',
            consoleUrl: 'https://console.example.test',
          },
        })}
      />,
      { transport },
    );
    expect(screen.getByText(label).closest('a')).toBeNull();
    expect(unary).not.toHaveBeenCalled();
  });
});
