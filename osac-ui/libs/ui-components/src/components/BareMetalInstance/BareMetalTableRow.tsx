import { FC, useMemo } from 'react';
import { Td, Tr } from '@patternfly/react-table';

import {
  BareMetalInstance,
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceType,
} from '@osac/types';
import { formatBareMetalCreatedAt } from '@osac/ui-components/components/BareMetalInstance/bareMetalInstanceDisplay';
import BareMetalInstanceResources from '@osac/ui-components/components/BareMetalInstance/BareMetalInstanceResources';
import {
  findBareMetalInstanceTypeForReference,
  findBareMetalInstanceTypeReference,
} from '@osac/ui-components/components/catalog/bareMetalCatalogItemResourceDisplay';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';

import { BareMetalActionsMenu } from './BareMetalActionsMenu';
import { BareMetalStatusLabel } from './BareMetalStatusLabel';

export type BareMetalTableColumn =
  | 'name'
  | 'status'
  | 'project'
  | 'configuration'
  | 'created'
  | 'actions';

interface BareMetalTableRowProps {
  instance: BareMetalInstance;
  instanceTypes: BareMetalInstanceType[];
  catalogItems: BareMetalInstanceCatalogItem[];
  columnLabels: Record<BareMetalTableColumn, string>;
}

export const BareMetalTableRow: FC<BareMetalTableRowProps> = ({
  instance,
  instanceTypes,
  catalogItems,
  columnLabels,
}) => {
  const instanceType = useMemo(() => {
    const reference = findBareMetalInstanceTypeReference(instance, catalogItems);
    return findBareMetalInstanceTypeForReference(instanceTypes, reference);
  }, [catalogItems, instance, instanceTypes]);

  return (
    <Tr key={instance.id}>
      <Td dataLabel={columnLabels.name}>
        <ResourceNameField
          resource={instance}
          detailsUrl={`/bare-metal/${instance.id}`}
          subTitle={instance.spec?.catalogItem?.name}
        />
      </Td>
      <Td dataLabel={columnLabels.status}>
        <BareMetalStatusLabel state={instance.status?.state} />
      </Td>
      <Td dataLabel={columnLabels.project}>{instance.metadata?.project ?? '—'}</Td>
      <Td dataLabel={columnLabels.configuration}>
        <BareMetalInstanceResources instance={instance} instanceType={instanceType} />
      </Td>
      <Td dataLabel={columnLabels.created}>{formatBareMetalCreatedAt(instance.metadata)}</Td>
      <Td dataLabel={columnLabels.actions} isActionCell>
        <BareMetalActionsMenu instance={instance} />
      </Td>
    </Tr>
  );
};
