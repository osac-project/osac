import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Label,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation.ts';
import {
  type CatalogItem,
  GLOBAL_TENANT_VALUE,
  catalogItemMetadataLabelEntries,
} from '../catalogItemDisplay.ts';
import { useCatalogItemResourceLookups } from '../catalogItemResourceLookups.ts';
import CatalogItemConfiguration from './CatalogItemConfiguration';
import { Timestamp } from '../../Primitives/Timestamp';

interface CatalogItemDetailContentProps {
  item: CatalogItem;
}

export const CatalogItemDetailContent = ({ item }: CatalogItemDetailContentProps) => {
  const { t } = useTranslation();
  const resourceLookups = useCatalogItemResourceLookups();
  const metadataLabels = catalogItemMetadataLabelEntries(item);
  const description = item.description?.trim() || item.metadata?.description?.trim();
  const tenant = item.metadata?.tenant?.trim();

  return (
    <Stack hasGutter>
      <StackItem>
        <Card>
          <CardTitle>{t('Details')}</CardTitle>
          <CardBody>
            <DescriptionList isHorizontal>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Catalog name')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {item.metadata?.name ?? '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>

              {description ? (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
                  <DescriptionListDescription>{description}</DescriptionListDescription>
                </DescriptionListGroup>
              ) : null}

              {metadataLabels.length > 0 ? (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Labels')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    <Flex flexWrap={{ default: 'wrap' }} gap={{ default: 'gapSm' }}>
                      {metadataLabels.map(({ key, value }) => (
                        <FlexItem key={`${item.id}-detail-label-${key}`}>
                          <Label variant="outline" color="grey" isCompact>
                            <b>{key}</b>
                            {': '}
                            {value}
                          </Label>
                        </FlexItem>
                      ))}
                    </Flex>
                  </DescriptionListDescription>
                </DescriptionListGroup>
              ) : null}
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <Label color={item.published ? 'green' : undefined}>
                    {item.published ? t('Live') : t('Unpublished')}
                  </Label>
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <Timestamp value={item.metadata?.creationTimestamp} />
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {tenant === GLOBAL_TENANT_VALUE ? t('Global public') : tenant || '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>
              {item.metadata?.project ? (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
                  <DescriptionListDescription>{item.metadata.project}</DescriptionListDescription>
                </DescriptionListGroup>
              ) : null}
            </DescriptionList>
          </CardBody>
        </Card>
      </StackItem>

      <StackItem>
        <Card>
          <CardTitle>{t('Configuration')}</CardTitle>
          <CardBody>
            <CatalogItemConfiguration item={item} resourceLookups={resourceLookups} />
          </CardBody>
        </Card>
      </StackItem>
    </Stack>
  );
};
