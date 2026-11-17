import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  ClusterCatalogItems,
  ClusterSchema,
  ClusterState,
  ProjectSchema,
  Projects,
} from '@osac/types';

import ClusterDetailsSummary from './ClusterDetailsSummary';
import { usePermissions } from '../../../hooks/use-permissions';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const ProjectPermissionsProbe = () => {
  const list = usePermissions(Projects, 'list');
  const get = usePermissions(Projects, 'get');
  return (
    <span>
      {list.isLoading || get.isLoading
        ? 'Checking project permissions'
        : 'Project permissions settled'}
    </span>
  );
};

describe('ClusterDetailsSummary', () => {
  it('puts status first, omits worker count and preserves plain-text URLs', () => {
    renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          id: 'cl-1',
          spec: { nodeSets: { compute: { size: 5 } } },
          status: {
            state: ClusterState.READY,
            apiUrl: 'https://api.example.com:6443',
            consoleUrl: 'https://console.example.com',
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
    expect(screen.getByText('https://api.example.com:6443').closest('a')).toBeNull();
    expect(screen.getByText('https://console.example.com').closest('a')).toBeNull();
  });

  it('links the encoded catalog ID without requiring VMaaS', async () => {
    renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          spec: { catalogItem: { id: 'catalog/one ?', name: 'Standard' } },
        })}
      />,
      { apiFixtures: { enabledServices: [] } },
    );
    expect(await screen.findByRole('link', { name: 'Standard' })).toHaveAttribute(
      'href',
      '/catalog/cluster/catalog%2Fone%20%3F',
    );
  });

  it('uses text for catalog names without an ID or denied Get permission', async () => {
    const transport = createMockConnectTransport(
      {},
      {
        onSelfSubjectAccessReview: () => ({ object: { status: { allowed: false } } }),
      },
    );
    const unary = vi.spyOn(transport, 'unary');
    const { rerender } = renderWithProviders(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          spec: { catalogItem: { name: 'Name only' } },
        })}
      />,
      { transport },
    );
    expect(screen.getByText('Name only').closest('a')).toBeNull();
    rerender(
      <ClusterDetailsSummary
        cluster={create(ClusterSchema, {
          spec: { catalogItem: { id: 'catalog-1', name: 'Denied' } },
        })}
      />,
    );
    await waitFor(() => expect(unary).toHaveBeenCalled());
    expect(screen.getByText('Denied').closest('a')).toBeNull();
    expect(unary.mock.calls.some(([method]) => method === ClusterCatalogItems.method.get)).toBe(
      false,
    );
  });

  it.each(['', 'division.team'])(
    'resolves canonical project path "%s" to its real encoded ID',
    async (path) => {
      const transport = createMockConnectTransport({
        projects: [
          create(ProjectSchema, {
            id: 'actual/id',
            metadata: { name: path, tenant: 'tenant-a' },
            spec: { title: 'Project title' },
          }),
        ],
      });
      const unary = vi.spyOn(transport, 'unary');
      renderWithProviders(
        <ClusterDetailsSummary
          cluster={create(ClusterSchema, {
            metadata: { project: path, tenant: 'tenant-a' },
          })}
        />,
        { transport, apiFixtures: { enabledServices: [] } },
      );
      expect(
        await screen.findByRole('link', { name: path ? 'Project title' : 'Root' }),
      ).toHaveAttribute('href', '/projects/actual%2Fid');
      const call = unary.mock.calls.find(([method]) => method === Projects.method.list);
      expect(call?.[4]).toHaveProperty(
        'filter',
        expect.stringContaining(`this.metadata.name == ${JSON.stringify(path)}`),
      );
      expect(call?.[4]).toHaveProperty(
        'filter',
        expect.stringContaining('this.metadata.tenant == "tenant-a"'),
      );
    },
  );

  it.each([
    'missing',
    'ambiguous',
    'other-tenant',
    'empty-id',
    'denied-list',
    'denied-get',
    'unavailable',
    'missing-permission-status',
    'default-permission',
  ])('keeps root project readable without a broken route when %s', async (scenario) => {
    const root = create(ProjectSchema, {
      id: scenario === 'empty-id' ? '' : 'real-root',
      metadata: { name: '', tenant: scenario === 'other-tenant' ? 'another-tenant' : 'tenant-a' },
    });
    const transport = createMockConnectTransport(
      {
        projects:
          scenario === 'missing'
            ? []
            : scenario === 'ambiguous'
              ? [root, create(ProjectSchema, { ...root, id: 'second-root' })]
              : [root],
      },
      {
        onSelfSubjectAccessReview: (request) => {
          if (scenario === 'unavailable') {
            throw new Error('Permission service unavailable');
          }
          if (scenario === 'missing-permission-status') {
            return { object: {} };
          }
          if (scenario === 'default-permission') {
            return { object: { status: {} } };
          }
          return {
            object: {
              status: {
                allowed: !(
                  request.object?.spec?.service === Projects.typeName &&
                  request.object?.spec?.method ===
                    (scenario === 'denied-list' ? 'List' : scenario === 'denied-get' ? 'Get' : '')
                ),
              },
            },
          };
        },
      },
    );
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(
      <>
        <ProjectPermissionsProbe />
        <ClusterDetailsSummary
          cluster={create(ClusterSchema, { metadata: { project: '', tenant: 'tenant-a' } })}
        />
      </>,
      { transport },
    );
    await screen.findByText('Project permissions settled');
    await waitFor(() => expect(unary).toHaveBeenCalled());
    if (
      !['denied-list', 'unavailable', 'missing-permission-status', 'default-permission'].includes(
        scenario,
      )
    ) {
      await waitFor(() =>
        expect(unary.mock.calls.some(([method]) => method === Projects.method.list)).toBe(true),
      );
    }
    expect(screen.getByText('Root').closest('a')).toBeNull();
    expect(screen.queryByRole('link', { name: 'Root' })).not.toBeInTheDocument();
  });

  it('renders missing project metadata without making a root lookup', async () => {
    const transport = createMockConnectTransport();
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(<ClusterDetailsSummary cluster={create(ClusterSchema)} />, { transport });
    await waitFor(() => expect(unary).toHaveBeenCalled());
    expect(screen.queryByText('Root')).not.toBeInTheDocument();
    expect(unary.mock.calls.some(([method]) => method === Projects.method.list)).toBe(false);
  });
});
