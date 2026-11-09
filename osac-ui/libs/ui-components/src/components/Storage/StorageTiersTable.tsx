import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { StorageBackend, StorageTier } from '@osac/types/private';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import StorageTierActionsMenu from './StorageTierActionsMenu';
import { protocolLabel, tierBackendDisplayNames } from './storageTierBackendResolution';
import { StorageTierStatusLabel } from './StorageTierStatusLabel';
import { storageTierDetailsPath } from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface StorageTiersTableProps {
  tiers: StorageTier[];
  backendsById: ReadonlyMap<string, StorageBackend>;
}

export const StorageTiersTable = ({ tiers, backendsById }: StorageTiersTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      backends: t('Backends'),
      protocol: t('Protocol'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  const protocolLabels = protocolLabel(t);

  return (
    <Table aria-label={t('Storage tiers')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.backends}</Th>
          <Th>{columnLabels.protocol}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {tiers.map((tier) => (
          <Tr key={tier.id}>
            <Td dataLabel={columnLabels.name}>
              <ResourceNameField resource={tier} detailsUrl={storageTierDetailsPath(tier.id)} />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <StorageTierStatusLabel state={tier.status?.state} />
            </Td>
            <Td dataLabel={columnLabels.backends}>{tierBackendDisplayNames(tier, backendsById)}</Td>
            <Td dataLabel={columnLabels.protocol}>
              {tier.spec?.protocol !== undefined ? protocolLabels[tier.spec.protocol] : '—'}
            </Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={tier.metadata?.creationTimestamp} />
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <StorageTierActionsMenu tier={tier} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
