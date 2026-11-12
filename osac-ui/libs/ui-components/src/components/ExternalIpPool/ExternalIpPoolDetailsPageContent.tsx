import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Grid,
  GridItem,
  PageSection,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { ExternalIPPool } from '@osac/types/private';

import ExternalIpPoolCidrsSection from './ExternalIpPoolCidrsSection';
import ExternalIpPoolStatusLabel from './ExternalIpPoolStatusLabel';
import { getPoolTenant } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { Timestamp } from '../Primitives/Timestamp';

interface ExternalIpPoolDetailsPageContentProps {
  pool: ExternalIPPool;
}

const ExternalIpPoolDetailsPageContent = ({ pool }: ExternalIpPoolDetailsPageContentProps) => {
  const { t } = useTranslation();
  const cidrs = pool.spec?.cidrs ?? [];
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
                      {getPoolTenant(pool, t)}
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
              <DescriptionList isCompact aria-label={t('External IP pool capacity')}>
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Available')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    {pool.status ? `${pool.status.available}` : displayValue()}
                  </DescriptionListDescription>
                </DescriptionListGroup>
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Total')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    {pool.status ? `${pool.status.total}` : displayValue()}
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
