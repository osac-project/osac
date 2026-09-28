import { create } from '@bufbuild/protobuf';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceTypeSchema,
  ClusterCatalogItemSchema,
  ClusterCatalogNodeSetSchema,
  ClusterNodeSetMapPolicySchema,
  HostTypeSchema,
} from '@osac/types';

import type { CatalogItemResourceLookups } from './catalogItemResourceLookups';
import { formatClusterCatalogHostTypeRow } from './clusterCatalogItemResourceDisplay';
import ClusterCatalogItemResources from './ClusterCatalogItemResources';

vi.mock('@osac/ui-components/hooks/useTranslation', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const workerType = create(BareMetalInstanceTypeSchema, {
  id: 'worker-type-id',
  metadata: { name: 'bare-metal-worker' },
});

const resourceLookups: CatalogItemResourceLookups = {
  diskImages: [],
  computeInstanceTypes: [],
  bareMetalInstanceTypes: [workerType],
  clusterVersions: [],
  hostTypes: [create(HostTypeSchema, { id: workerType.id, title: 'Unrelated legacy host type' })],
};

const workerNodeSet = create(ClusterCatalogNodeSetSchema, {
  size: 2,
  baremetalInstanceType: { id: workerType.id },
});

const nodeSetMap = { items: { 'compute-workers': workerNodeSet } };

describe('ClusterCatalogItemResources', () => {
  it.each(['locked', 'editable'] as const)(
    'resolves the bare-metal instance type for a %s node-set policy',
    (behavior) => {
      const nodeSets = create(ClusterNodeSetMapPolicySchema, {
        behavior:
          behavior === 'locked'
            ? { case: 'locked', value: nodeSetMap }
            : { case: 'editable', value: { defaultValue: nodeSetMap } },
      });
      const catalogItem = create(ClusterCatalogItemSchema, { fields: { nodeSets } });

      render(
        <ClusterCatalogItemResources catalogItem={catalogItem} resourceLookups={resourceLookups} />,
      );

      expect(screen.getByText('Compute Workers')).toBeInTheDocument();
      expect(screen.getByText('bare-metal-worker')).toBeInTheDocument();
      expect(screen.queryByText('Unrelated legacy host type')).not.toBeInTheDocument();
    },
  );

  it.each([{ name: 'unresolved-worker' }, { id: 'unresolved-worker' }])(
    'shows an unresolved bare-metal reference %j instead of an empty row',
    (reference) => {
      const nodeSets = create(ClusterNodeSetMapPolicySchema, {
        behavior: {
          case: 'locked',
          value: { items: { compute: { size: 1, baremetalInstanceType: reference } } },
        },
      });
      const catalogItem = create(ClusterCatalogItemSchema, { fields: { nodeSets } });

      render(
        <ClusterCatalogItemResources catalogItem={catalogItem} resourceLookups={resourceLookups} />,
      );

      expect(screen.getByText('unresolved-worker')).toBeInTheDocument();
    },
  );

  it('does not invent a worker label for an unconfigured or empty node-set policy', () => {
    const emptyPolicy = create(ClusterNodeSetMapPolicySchema, {
      behavior: { case: 'locked', value: { items: {} } },
    });

    expect(
      formatClusterCatalogHostTypeRow(
        undefined,
        { key: 'compute', nodeSet: workerNodeSet },
        workerType,
      ),
    ).toBeUndefined();
    expect(formatClusterCatalogHostTypeRow(emptyPolicy, undefined, workerType)).toBeUndefined();
  });
});
