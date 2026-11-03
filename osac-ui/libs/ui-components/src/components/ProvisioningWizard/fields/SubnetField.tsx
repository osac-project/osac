import { useField } from 'formik';

import { type Subnet, Subnets } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { virtualNetworkFilterForSubnetList } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { type ResourceListItem, ResourceSelectField } from '../../Form/ResourceSelectField';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface SubnetFieldProps extends ProvisioningFieldControlProps {
  virtualNetworkName: string;
  isRequired?: boolean;
}

const getSubnetCidr = (item: ResourceListItem): string | undefined =>
  (item as Subnet).spec?.ipv4Cidr;

const SubnetField = ({
  name,
  virtualNetworkName,
  isRequired = false,
  isDisabled = false,
}: SubnetFieldProps) => {
  const { t } = useTranslation();
  const [virtualNetwork] = useField<ResourceSelectValue>(virtualNetworkName);
  const virtualNetworkId = virtualNetwork.value?.id ?? '';

  return (
    <ResourceSelectField
      name={name}
      label={t('Subnet')}
      fieldId={name}
      service={Subnets}
      request={
        virtualNetworkId ? { filter: virtualNetworkFilterForSubnetList(virtualNetworkId) } : {}
      }
      isDisabled={isDisabled || !virtualNetworkId}
      autoSelectSingleOption
      isRequired={isRequired}
      getOptionDescription={getSubnetCidr}
      placeholder={t('Select subnet')}
      loadErrorTitle={t('Could not load subnets')}
    />
  );
};

export default SubnetField;
