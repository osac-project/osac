import { Content, DescriptionList } from '@patternfly/react-core';

import type { BareMetalInstanceCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import {
  bareMetalCatalogItemFields,
  bareMetalDiskImageReferenceFromPolicy,
  bareMetalInstanceTypeReferenceFromPolicy,
  formatBareMetalCatalogDiskImage,
} from '../../catalog/bareMetalCatalogItemResourceDisplay';
import {
  CatalogConfigurationRow,
  hasConfiguredPolicy,
} from '../../CatalogItem/Details/CatalogConfigurationRow';
import CatalogUserDataDetailValue from '../../CatalogItem/Details/CatalogUserDataDetailValue';

interface BareMetalInstanceCatalogItemConfigurationProps {
  item: BareMetalInstanceCatalogItem;
}

const BareMetalInstanceCatalogItemConfiguration = ({
  item,
}: BareMetalInstanceCatalogItemConfigurationProps) => {
  const { t } = useTranslation();
  const fields = bareMetalCatalogItemFields(item);
  const instanceTypeReference = bareMetalInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = bareMetalDiskImageReferenceFromPolicy(fields?.diskImage);
  const hasConfiguredField = hasConfiguredPolicy(
    fields?.instanceType,
    fields?.diskImage,
    fields?.userDataSource,
  );

  if (!hasConfiguredField) {
    return <Content component="p">{t('No governed configuration')}</Content>;
  }

  return (
    <DescriptionList isCompact>
      <CatalogConfigurationRow
        label={t('Instance type')}
        value={instanceTypeReference?.name || undefined}
        policy={fields?.instanceType}
      />
      <CatalogConfigurationRow
        label={t('Disk image')}
        value={formatBareMetalCatalogDiskImage(fields, undefined, diskImageReference)}
        policy={fields?.diskImage}
      />
      <CatalogConfigurationRow
        label={t('User data')}
        valueContent={<CatalogUserDataDetailValue policy={fields?.userDataSource} />}
        policy={fields?.userDataSource}
      />
    </DescriptionList>
  );
};

export default BareMetalInstanceCatalogItemConfiguration;
