import { useMemo } from 'react';
import { Alert, Stack, StackItem } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { StorageTiers } from '@osac/types/private';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import {
  storageBackendIdsFilter,
  usePrivateStorageBackends,
} from '../../api/v1/private/storage-backends';
import { usePrivateStorageTiers } from '../../api/v1/private/storage-tiers';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import StorageTierActionsMenu from '../../components/Storage/StorageTierActionsMenu';
import {
  protocolLabel,
  uniqueBackendIds,
} from '../../components/Storage/storageTierBackendResolution';
import { StorageTierStatusLabel } from '../../components/Storage/StorageTierStatusLabel';
import { SubtleContent } from '../../components/SubtleContent/SubtleContent';
import { useTranslation } from '../../hooks/useTranslation';

export const StorageTiersListPage = () => {
  const { t } = useTranslation();

  const { data: tiers = [], isLoading, error } = usePrivateStorageTiers();

  const backendIds = useMemo(() => uniqueBackendIds(tiers), [tiers]);

  const { data: backends = [], error: backendsError } = usePrivateStorageBackends(
    { filter: storageBackendIdsFilter(backendIds) },
    { enabled: backendIds.length > 0 },
  );

  const backendsById = useMemo(
    () => new Map(backends.map((backend) => [backend.id, backend])),
    [backends],
  );

  const protocolLabels = protocolLabel(t);

  return (
    <ListPage
      title={t('Storage Tiers')}
      description={t('Manage storage tiers for this cloud platform.')}
      actions={
        <CreateButton service={StorageTiers} to="/admin/infrastructure/storage/tiers/create">
          {t('Create tier')}
        </CreateButton>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Stack hasGutter>
          {Boolean(backendsError) && (
            <StackItem>
              <Alert variant="warning" isInline title={t('Unable to resolve backend names')}>
                {t(
                  'Backend IDs are shown in place of names until this recovers. This is separate from the normal fallback shown when a tier references a backend that no longer exists.',
                )}
              </Alert>
            </StackItem>
          )}
          <StackItem>
            {tiers.length === 0 ? (
              <SubtleContent component="p">
                {t('No storage tiers yet. Create one to get started.')}
              </SubtleContent>
            ) : (
              <Table aria-label={t('Storage tiers')} variant="compact">
                <Thead>
                  <Tr>
                    <Th>{t('Name')}</Th>
                    <Th>{t('Status')}</Th>
                    <Th>{t('Backends')}</Th>
                    <Th>{t('Protocol')}</Th>
                    <Th aria-label={t('Actions')} />
                  </Tr>
                </Thead>
                <Tbody>
                  {tiers.map((tier) => {
                    const backendAssociations = tier.spec?.backends ?? [];
                    return (
                      <Tr key={tier.id}>
                        <Td dataLabel={t('Name')}>
                          <ResourceNameField
                            resource={tier}
                            detailsUrl={`/admin/infrastructure/storage/tiers/${tier.id}`}
                          />
                        </Td>
                        <Td dataLabel={t('Status')}>
                          <StorageTierStatusLabel state={tier.status?.state} />
                        </Td>
                        <Td dataLabel={t('Backends')}>
                          {backendAssociations
                            .map(
                              (association) =>
                                backendsById.get(association.backendId)?.metadata?.name ??
                                association.backendId,
                            )
                            .join(', ')}
                        </Td>
                        <Td dataLabel={t('Protocol')}>
                          {tier.spec?.protocol !== undefined
                            ? protocolLabels[tier.spec.protocol]
                            : '—'}
                        </Td>
                        <Td dataLabel={t('Actions')} isActionCell>
                          <StorageTierActionsMenu tier={tier} />
                        </Td>
                      </Tr>
                    );
                  })}
                </Tbody>
              </Table>
            )}
          </StackItem>
        </Stack>
      </ListPageBody>
    </ListPage>
  );
};
