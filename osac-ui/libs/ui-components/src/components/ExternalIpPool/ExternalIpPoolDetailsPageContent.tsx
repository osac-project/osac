import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Grid,
  GridItem,
  PageSection,
  Progress,
  ProgressMeasureLocation,
  ProgressVariant,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { ExternalIPPool } from '@osac/types/private';

import ExternalIpPoolCidrsSection from './ExternalIpPoolCidrsSection';
import ExternalIpPoolStatusLabel from './ExternalIpPoolStatusLabel';
import { poolCapacity } from './poolCapacity';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { Timestamp } from '../Primitives/Timestamp';

const SHARED_TENANT = 'shared';

interface ExternalIpPoolDetailsPageContentProps {
  pool: ExternalIPPool;
}

const ExternalIpPoolDetailsPageContent = ({ pool }: ExternalIpPoolDetailsPageContentProps) => {
  const { t } = useTranslation();
  const cidrs = pool.spec?.cidrs ?? [];
  const capacity = poolCapacity(pool);
  const tenant = pool.metadata?.tenant;

  return (
    <PageSection hasBodyWrapper={false}>
      <Grid hasGutter>
        <GridItem md={4}>
          <Stack hasGutter>
            <StackItem>
              <Title headingLevel="h2" size="lg">
                {t('Overview')}
              </Title>
            </StackItem>
            <StackItem>
              <DescriptionList isCompact aria-label={t('External IP pool overview')}>
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    <ExternalIpPoolStatusLabel state={pool.status?.state} />
                  </DescriptionListDescription>
                </DescriptionListGroup>
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    <Timestamp value={pool.metadata?.creationTimestamp} />
                  </DescriptionListDescription>
                </DescriptionListGroup>
                {tenant ? (
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {tenant === SHARED_TENANT ? t('Shared') : tenant}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                ) : null}
              </DescriptionList>
            </StackItem>
          </Stack>
        </GridItem>

        <GridItem md={4}>
          <Stack hasGutter>
            <StackItem>
              <Title headingLevel="h2" size="lg">
                {t('Capacity')}
              </Title>
            </StackItem>
            <StackItem>
              {capacity ? (
                <Progress
                  title={t('Used')}
                  value={capacity.usedPercent}
                  measureLocation={ProgressMeasureLocation.outside}
                  label={t('{{used}} of {{total}} used', {
                    used: `${capacity.used}`,
                    total: `${capacity.total}`,
                  })}
                  // Nearly full pools stand out before they block new external IPs.
                  variant={
                    capacity.available === 0n
                      ? ProgressVariant.danger
                      : capacity.usedPercent >= 80
                        ? ProgressVariant.warning
                        : undefined
                  }
                  aria-label={t('External IP pool capacity')}
                />
              ) : null}
              <DescriptionList isCompact aria-label={t('External IP pool capacity')}>
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Available')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    {capacity
                      ? t('{{available}} of {{total}} ({{percent}}%)', {
                          available: `${capacity.available}`,
                          total: `${capacity.total}`,
                          percent: capacity.availablePercent,
                        })
                      : displayValue()}
                  </DescriptionListDescription>
                </DescriptionListGroup>
              </DescriptionList>
            </StackItem>
          </Stack>
        </GridItem>

        <GridItem md={4}>
          <ExternalIpPoolCidrsSection cidrs={cidrs} />
        </GridItem>
      </Grid>
    </PageSection>
  );
};

export default ExternalIpPoolDetailsPageContent;
