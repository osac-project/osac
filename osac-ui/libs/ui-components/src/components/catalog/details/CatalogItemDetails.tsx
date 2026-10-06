import { useNavigate } from 'react-router-dom';
import { Button, Flex, FlexItem, PageSection, Stack, StackItem } from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import { CatalogItem, getCatalogCreateActionPath } from '../catalogItemDisplay';
import { CatalogItemDetailContent } from './CatalogItemDetailContent';

interface CatalogItemDetailsProps {
  item: CatalogItem;
}

const CatalogItemDetails = ({ item }: CatalogItemDetailsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();

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
                  parentTo="/catalog"
                  parentLabel={t('Catalog')}
                  resourceName={item.metadata?.name || item.title}
                />
              </FlexItem>
              <FlexItem>
                <Button
                  variant="primary"
                  isDisabled={!item.published}
                  onClick={() => navigate(getCatalogCreateActionPath(item))}
                >
                  {t('Launch instance')}
                </Button>
              </FlexItem>
            </Flex>
          </StackItem>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        <CatalogItemDetailContent item={item} />
      </PageSection>
    </>
  );
};

export default CatalogItemDetails;
