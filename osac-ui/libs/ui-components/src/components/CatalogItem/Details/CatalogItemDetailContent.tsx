import type { ReactNode } from 'react';
import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Grid,
  GridItem,
  Label,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation.ts';
import {
  type CatalogItem,
  GLOBAL_TENANT_VALUE,
  catalogItemTypeBadgeLabel,
} from '../../catalog/catalogItemDisplay.ts';
import { Timestamp } from '../../Primitives/Timestamp';
interface CatalogItemDetailContentProps {
  item: CatalogItem;
  configuration: ReactNode;
}

export const CatalogItemDetailContent = ({
  item,
  configuration,
}: CatalogItemDetailContentProps) => {
  const { t } = useTranslation();

  const tenant = item.metadata?.tenant?.trim();
  const configurationTitleId = 'catalog-item-configuration-card-title';

  return (
    <Grid hasGutter>
      <GridItem md={6} lg={8}>
        <Card component="section" isFullHeight>
          <CardBody>
            <Grid hasGutter>
              <GridItem md={6}>
                <Stack hasGutter>
                  <StackItem>
                    <Title headingLevel="h2" size="lg">
                      {t('Overview')}
                    </Title>
                  </StackItem>
                  <StackItem>
                    <DescriptionList isCompact>
                      <DescriptionListGroup>
                        <DescriptionListTerm>{t('Service')}</DescriptionListTerm>
                        <DescriptionListDescription>
                          {catalogItemTypeBadgeLabel(item, t)}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
                        <DescriptionListDescription>
                          <Label color={item.published ? 'green' : undefined} isCompact>
                            {item.published ? t('Live') : t('Unpublished')}
                          </Label>
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                    </DescriptionList>
                  </StackItem>
                </Stack>
              </GridItem>

              <GridItem md={6}>
                <Stack hasGutter>
                  <StackItem>
                    <Title headingLevel="h2" size="lg">
                      {t('Publishing')}
                    </Title>
                  </StackItem>
                  <StackItem>
                    <DescriptionList isCompact>
                      <DescriptionListGroup>
                        <DescriptionListTerm>{t('Visibility')}</DescriptionListTerm>
                        <DescriptionListDescription>
                          {tenant === GLOBAL_TENANT_VALUE ? t('Global public') : tenant || '—'}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                        <DescriptionListDescription>
                          <Timestamp value={item.metadata?.creationTimestamp} />
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                    </DescriptionList>
                  </StackItem>
                </Stack>
              </GridItem>
            </Grid>
          </CardBody>
        </Card>
      </GridItem>

      <GridItem md={6} lg={4}>
        <Card
          component="section"
          variant="secondary"
          isFullHeight
          aria-labelledby={configurationTitleId}
        >
          <CardTitle id={configurationTitleId}>{t('Configuration')}</CardTitle>
          <CardBody>{configuration}</CardBody>
        </Card>
      </GridItem>
    </Grid>
  );
};
