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
import { Timestamp } from '../../Primitives/Timestamp';
import {
  type CatalogItem,
  GLOBAL_TENANT_VALUE,
  catalogItemTypeBadgeLabel,
} from '../catalogItemDisplay.ts';
import { useCatalogItemResourceLookups } from '../catalogItemResourceLookups.ts';
import CatalogItemConfiguration from './CatalogItemConfiguration';

interface CatalogItemDetailContentProps {
  item: CatalogItem;
}

export const CatalogItemDetailContent = ({ item }: CatalogItemDetailContentProps) => {
  const { t } = useTranslation();
  const resourceLookups = useCatalogItemResourceLookups();

  const tenant = item.metadata?.tenant?.trim();
  const detailsTitleId = 'catalog-item-details-card-title';
  const configurationTitleId = 'catalog-item-configuration-card-title';

  return (
    <Grid hasGutter>
      <GridItem md={6} lg={8}>
        <Card component="section" isFullHeight aria-labelledby={detailsTitleId}>
          <CardTitle id={detailsTitleId}>{t('Details')}</CardTitle>
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
          <CardBody>
            <CatalogItemConfiguration item={item} resourceLookups={resourceLookups} />
          </CardBody>
        </Card>
      </GridItem>
    </Grid>
  );
};
