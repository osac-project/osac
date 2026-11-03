import { useField } from 'formik';

import { type Subnet, Subnets } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useVmSubnetValidator } from './useVmSubnetValidator';
import { virtualNetworkFilterForSubnetList } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { type ResourceListItem, ResourceSelectField } from '../../Form/ResourceSelectField';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface SubnetFieldProps extends ProvisioningFieldControlProps {
  virtualNetworkName: string;
  isRequired?: boolean;
  requireVmSupport?: boolean;
}

const getSubnetCidr = (item: ResourceListItem): string | undefined =>
  (item as Subnet).spec?.ipv4Cidr;

const SubnetField = ({
  name,
  virtualNetworkName,
  isRequired = false,
  isDisabled = false,
  requireVmSupport = false,
}: SubnetFieldProps) => {
  const { t } = useTranslation();
  const [virtualNetwork] = useField<ResourceSelectValue>(virtualNetworkName);
  const virtualNetworkId = virtualNetwork.value?.id ?? '';
  const validateVmSubnet = useVmSubnetValidator();

  return (
    <ResourceSelectField
      name={name}
      label={t('Subnet')}
      fieldId={name}
      service={Subnets}
      request={
        virtualNetworkId || requireVmSupport
          ? {
              filter: virtualNetworkFilterForSubnetList(
                virtualNetworkId || undefined,
                requireVmSupport,
              ),
            }
          : {}
      }
      isDisabled={isDisabled || !virtualNetworkId}
      validateSelection={
        requireVmSupport
          ? (value, _items, state) =>
              value?.id && (state.isFetching || state.hasError)
                ? t('A subnet must be READY and support compute instances.')
                : validateVmSubnet(value, virtualNetworkId)
          : undefined
      }
      autoSelectSingleOption
      isRequired={isRequired}
      getOptionDescription={getSubnetCidr}
      placeholder={t('Select subnet')}
      emptyTitle={requireVmSupport ? t('No VM-capable subnets available') : undefined}
      emptyDescription={
        requireVmSupport ? t('A subnet must be READY and support compute instances.') : undefined
      }
      loadErrorTitle={t('Could not load subnets')}
    />
  );
};

export default SubnetField;
