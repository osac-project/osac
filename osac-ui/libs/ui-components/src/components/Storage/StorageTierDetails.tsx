import { useMemo } from 'react';
import {
  Alert,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { StorageTier } from '@osac/types/private';

import { StorageTierBackendAssociationsTable } from './StorageTierBackendAssociationsTable';
import { protocolLabel, uniqueBackendIds } from './storageTierBackendResolution';
import { StorageTierDetailActionButtons } from './StorageTierDetailActionButtons';
import { StorageTierStatusLabel } from './StorageTierStatusLabel';
import {
  storageBackendIdsFilter,
  usePrivateStorageBackends,
} from '../../api/v1/private/storage-backends';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { getErrorMessage } from '../../utils/error';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';

const TIERS_LIST_PATH = '/admin/infrastructure/storage/tiers';

interface StorageTierDetailsProps {
  tier: StorageTier;
}

export const StorageTierDetails = ({ tier }: StorageTierDetailsProps) => {
  const { t } = useTranslation();
  const tierName = tier.metadata?.name ?? tier.id;
  const protocolLabels = protocolLabel(t);

  const backendIds = useMemo(() => uniqueBackendIds([tier]), [tier]);
  const { data: backends = [], error: backendsError } = usePrivateStorageBackends(
    { filter: storageBackendIdsFilter(backendIds) },
    { enabled: backendIds.length > 0 },
  );
  const backendsById = useMemo(
    () => new Map(backends.map((backend) => [backend.id, backend])),
    [backends],
  );

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parentTo={TIERS_LIST_PATH}
                  parentLabel={t('Storage tiers')}
                  resourceName={tierName}
                  titleAddon={<StorageTierStatusLabel state={tier.status?.state} />}
                />
              </FlexItem>
              <FlexItem>
                <StorageTierDetailActionButtons tier={tier} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Card>
          <CardBody>
            <DescriptionList isHorizontal>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
                <DescriptionListDescription>{tierName}</DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(tier.spec?.description)}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Protocol')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {tier.spec ? protocolLabels[tier.spec.protocol] : '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>

              {tier.status?.message && (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Message')}</DescriptionListTerm>
                  <DescriptionListDescription>{tier.status.message}</DescriptionListDescription>
                </DescriptionListGroup>
              )}
            </DescriptionList>
          </CardBody>
        </Card>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Card>
          <CardTitle>{t('Backend associations')}</CardTitle>
          <CardBody>
            <Stack hasGutter>
              {Boolean(backendsError) && (
                <StackItem>
                  <Alert variant="danger" isInline title={t('Failed to fetch storage backends')}>
                    {getErrorMessage(backendsError)}
                  </Alert>
                </StackItem>
              )}
              <StackItem>
                <StorageTierBackendAssociationsTable
                  backends={tier.spec?.backends ?? []}
                  backendsById={backendsById}
                />
              </StackItem>
            </Stack>
          </CardBody>
        </Card>
      </PageSection>
    </>
  );
};
