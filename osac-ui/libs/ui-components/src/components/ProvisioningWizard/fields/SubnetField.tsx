import { useField } from 'formik';

import { Subnets } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { virtualNetworkFilterForSubnetList } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceSelectField } from '../../Form/ResourceSelectField';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface SubnetFieldProps extends ProvisioningFieldControlProps {
  virtualNetworkName: string;
  isRequired?: boolean;
  showCidr?: boolean;
}

const SubnetField = ({
  name,
  virtualNetworkName,
  isRequired = false,
  showCidr = false,
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
      showIpv4Cidr={showCidr}
      placeholder={t('Select subnet')}
      loadErrorTitle={t('Could not load subnets')}
    />
  );
};

export default SubnetField;
