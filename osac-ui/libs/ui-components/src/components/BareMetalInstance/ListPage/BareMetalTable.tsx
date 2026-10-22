import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { BareMetalInstance } from '@osac/types';
import { BareMetalStatusLabel } from '@osac/ui-components/components/BareMetalInstance/BareMetalStatusLabel';
import { BareMetalActionsMenu } from '@osac/ui-components/components/BareMetalInstance/ListPage/BareMetalActionsMenu';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

interface BareMetalTableProps {
  instances: BareMetalInstance[];
}

export const BareMetalTable = ({ instances }: BareMetalTableProps) => {
  const { t } = useTranslation();
  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      project: t('Project'),
      instanceType: t('Instance type'),
      diskImage: t('Disk image'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Bare metal instances')} variant="compact" borders>
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.project}</Th>
          <Th>{columnLabels.instanceType}</Th>
          <Th>{columnLabels.diskImage}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {instances.map((instance) => (
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
            <Td dataLabel={columnLabels.project}>{instance.metadata?.project || t('Default')}</Td>
            <Td dataLabel={columnLabels.instanceType}>
              {instance.spec?.instanceType?.name || '—'}
            </Td>
            <Td dataLabel={columnLabels.diskImage}>{instance.spec?.diskImage?.name || '—'}</Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={instance.metadata?.creationTimestamp} />
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <BareMetalActionsMenu instance={instance} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
