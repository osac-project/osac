import { useMemo } from 'react';
import { Alert, Stack, StackItem } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { StorageBackend, StorageBackends, StorageTiers } from '@osac/types/private';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

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

  const { data: tiers, isLoading, error } = useListResource(StorageTiers);

  const backendIds = useMemo(() => uniqueBackendIds(tiers?.items || []), [tiers?.items]);

  const { data: backends, error: backendsError } = useListResource(
    StorageBackends,
    { filter: cel<StorageBackend>((filter) => filter.field('id').isIn(backendIds)) },
    { enabled: backendIds.length > 0 },
  );

  const backendsById = useMemo(
    () => new Map((backends?.items || []).map((backend) => [backend.id, backend])),
    [backends?.items],
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
            {!tiers?.items.length ? (
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
                  {(tiers?.items || []).map((tier) => {
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
