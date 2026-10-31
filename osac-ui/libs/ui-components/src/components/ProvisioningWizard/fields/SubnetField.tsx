import { useField } from 'formik';

import { Subnets } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { virtualNetworkFilterForSubnetList } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceSelectField } from '../../Form/ResourceSelectField';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface SubnetFieldProps extends ProvisioningFieldControlProps {
  virtualNetworkName: string;
  requireVmSupport?: boolean;
}

const SubnetField = ({
  name,
  virtualNetworkName,
  isDisabled = false,
  requireVmSupport = false,
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
        requireVmSupport && isDisabled
          ? (value, items) =>
              value.id && !items.some((item) => item.id === value.id)
                ? t('A subnet must be READY and support compute instances.')
                : undefined
          : undefined
      }
      autoSelectSingleOption
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
