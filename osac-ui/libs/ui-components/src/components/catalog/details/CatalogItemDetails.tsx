import type { ReactNode } from 'react';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Divider,
  Flex,
  FlexItem,
  Label,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { CatalogItemDetailContent } from './CatalogItemDetailContent';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceButton from '../../Resource/DeleteResourceButton';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import { canManageCatalogItem } from '../authoring/canManageCatalogItem';
import CatalogItemDeleteModal from '../authoring/CatalogItemDeleteModal';
import { useSetCatalogItemPublished } from '../authoring/useSetCatalogItemPublished';
import {
  type CatalogItem,
  catalogItemEditPath,
  getCatalogCreateActionPath,
} from '../catalogItemDisplay';

interface CatalogItemDetailsProps {
  item: CatalogItem;
  children?: ReactNode;
  showStatus?: boolean;
  titleAddon?: ReactNode;
  titlePrefix?: ReactNode;
}

const CatalogItemDetails = ({
  item,
  children,
  showStatus = true,
  titleAddon,
  titlePrefix,
}: CatalogItemDetailsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { role, tenantId } = useSession();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { isPending, setPublished } = useSetCatalogItemPublished(item);
  const canManage = canManageCatalogItem(role, tenantId, item);
  const canLaunch = (role === 'tenant-admin' || role === 'tenant-user') && item.published;
  const resourceName = item.metadata?.name || item.title;

  return (
    <>
      {deleteOpen ? (
        <CatalogItemDeleteModal
          item={item}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate('/catalog')}
        />
      ) : null}
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
                  resourceName={resourceName}
                  titlePrefix={titlePrefix}
                  titleAddon={
                    showStatus
                      ? (titleAddon ?? (
                          <Label color={item.published ? 'green' : undefined}>
                            {item.published ? t('Live') : t('Unpublished')}
                          </Label>
                        ))
                      : titleAddon
                  }
                />
              </FlexItem>
              <FlexItem>
                <Flex spaceItems={{ default: 'spaceItemsSm' }}>
                  {canManage ? (
                    <FlexItem>
                      <Button
                        variant="secondary"
                        onClick={() => navigate(catalogItemEditPath(item))}
                      >
                        {t('Edit')}
                      </Button>
                    </FlexItem>
                  ) : null}
                  {canManage ? (
                    <FlexItem>
                      <Button
                        variant="secondary"
                        isDisabled={isPending}
                        onClick={() => setPublished(!item.published)}
                      >
                        {item.published ? t('Unpublish') : t('Publish')}
                      </Button>
                    </FlexItem>
                  ) : null}
                  {canManage ? (
                    <FlexItem>
                      <DeleteResourceButton onClick={() => setDeleteOpen(true)} />
                    </FlexItem>
                  ) : null}
                  {role === 'tenant-admin' || role === 'tenant-user' ? (
                    <FlexItem>
                      <Button
                        variant="primary"
                        isDisabled={!canLaunch}
                        onClick={() => navigate(getCatalogCreateActionPath(item))}
                      >
                        {t('Launch instance')}
                      </Button>
                    </FlexItem>
                  ) : null}
                </Flex>
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        {children ?? <CatalogItemDetailContent item={item} />}
      </PageSection>
    </>
  );
};

export default CatalogItemDetails;
