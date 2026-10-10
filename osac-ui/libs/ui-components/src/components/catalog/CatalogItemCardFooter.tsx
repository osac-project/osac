import { FC } from 'react';
import { CardFooter, Content, Flex, FlexItem } from '@patternfly/react-core';
import { RhUiLockFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-lock-fill-icon';
import { TFunction } from 'i18next';

import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';
import CatalogItemLaunchButton from '@osac/ui-components/components/catalog/CatalogItemLaunchButton';
import CatalogItemTenant from '@osac/ui-components/components/catalog/CatalogItemTenant';
import FieldSeparator from '@osac/ui-components/components/Primitives/FieldSeparator';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { UserRole } from '@osac/ui-components/shellTypes';

type CatalogItemCardFooterProps = {
  catalogItem: CatalogItem;
  role: UserRole;
  isWizardMode: boolean;
};

const getConfiguredString = (catalogItem: CatalogItem, t: TFunction) => {
  switch (catalogItem.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return t('Virtual machine pre-configured');
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return t('Hardware pre-configured');
    case 'osac.public.v1.ClusterCatalogItem':
      return t('Cluster pre-configured');
    default:
      return undefined;
  }
};

const CatalogItemCardFooter: FC<CatalogItemCardFooterProps> = ({
  catalogItem,
  role,
  isWizardMode,
}) => {
  const { t } = useTranslation();

  const renderFooterText = () => {
    if (role === 'tenant-user') {
      const configured = getConfiguredString(catalogItem, t);
      return (
        <Flex flexWrap={{ default: 'nowrap' }} spaceItems={{ default: 'spaceItemsXs' }}>
          <FlexItem>
            <RhUiLockFillIcon />
          </FlexItem>
          {configured ? (
            <>
              <FlexItem>{configured}</FlexItem>
              <FlexItem>
                <FieldSeparator />
              </FlexItem>
            </>
          ) : null}
          <FlexItem>{t('Admin-managed')}</FlexItem>
        </Flex>
      );
    }

    if (role === 'tenant-admin') {
      return (
        <Flex flexWrap={{ default: 'nowrap' }} spaceItems={{ default: 'spaceItemsXs' }}>
          <FlexItem>{catalogItem.metadata?.creator || '-'}</FlexItem>
          <FlexItem>
            <FieldSeparator />
          </FlexItem>
          <FlexItem>
            <Timestamp value={catalogItem.metadata?.creationTimestamp} format="Date" />
          </FlexItem>
        </Flex>
      );
    }

    return catalogItem.metadata?.tenant?.length ? (
      <CatalogItemTenant catalogItem={catalogItem} />
    ) : (
      '-'
    );
  };

  return (
    <CardFooter>
      <Flex direction={{ default: 'column' }} gap={{ default: 'gapMd' }}>
        <FlexItem>
          <Content component="small" className="pf-v6-u-color-text-subtle">
            {renderFooterText()}
          </Content>
        </FlexItem>
        {!isWizardMode && role !== 'admin' ? (
          <FlexItem>
            <CatalogItemLaunchButton
              isDisabled={!catalogItem.published}
              className="pf-v6-u-w-100"
              catalogItem={catalogItem}
            />
          </FlexItem>
        ) : null}
      </Flex>
    </CardFooter>
  );
};

export default CatalogItemCardFooter;
