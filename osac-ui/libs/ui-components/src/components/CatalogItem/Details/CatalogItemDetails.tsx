import type { ReactNode } from 'react';
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

import { type ServiceWithMethod, usePermissions } from '../../../hooks/use-permissions';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import {
  type CatalogItem,
  catalogItemEditPath,
  getCatalogCreateActionPath,
} from '../../catalog/catalogItemDisplay';
import DeleteResourceButton from '../../Resource/DeleteResourceButton';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import { useSetCatalogItemPublished } from '../useSetCatalogItemPublished';

interface CatalogItemDetailsProps {
  item: CatalogItem;
  children: ReactNode;
  service: ServiceWithMethod<'update' | 'delete'>;
  onDelete: VoidFunction;
  showStatus?: boolean;
  titleAddon?: ReactNode;
  titlePrefix?: ReactNode;
}

const CatalogItemDetails = ({
  item,
  children,
  service,
  onDelete,
  showStatus = true,
  titleAddon,
  titlePrefix,
}: CatalogItemDetailsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { role } = useSession();
  const { isPending, setPublished } = useSetCatalogItemPublished(item);
  const { allowed: canUpdate } = usePermissions(service, 'update');
  const { allowed: canDelete } = usePermissions(service, 'delete');
  const canManage = canUpdate && canDelete;
  const canLaunch = (role === 'tenant-admin' || role === 'tenant-user') && item.published;
  const resourceName = item.metadata?.name || '';

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
                  parents={[
                    {
                      to: '/catalog',
                      label: t('Catalog'),
                    },
                  ]}
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
                      <DeleteResourceButton onClick={onDelete} />
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
      <PageSection hasBodyWrapper={false}>{children}</PageSection>
    </>
  );
};

export default CatalogItemDetails;
