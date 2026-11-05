import { useFormikContext } from 'formik';

import { type VirtualNetwork, VirtualNetworks } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { cel } from '../../../api/cel';
import { VIRTUAL_NETWORK_READY_LIST_FILTER } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceSelectField } from '../../Form/ResourceSelectField';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

interface VirtualNetworkFieldProps extends ProvisioningFieldControlProps {
  subnetName: string;
  securityGroupsName: string;
  isRequired?: boolean;
  onCreate?: () => void;
  /** Set for a cloud provider admin: limits the list to the workload's tenant. */
  tenant?: string;
}

const VirtualNetworkField = ({
  name,
  subnetName,
  securityGroupsName,
  isRequired = false,
  onCreate,
  isDisabled = false,
  tenant,
}: VirtualNetworkFieldProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();

  return (
    <ResourceSelectField
      name={name}
      label={t('Virtual network')}
      fieldId={name}
      service={VirtualNetworks}
      request={{
        filter:
          tenant === undefined
            ? VIRTUAL_NETWORK_READY_LIST_FILTER
            : cel<VirtualNetwork>((filter) =>
                filter.and(
                  VIRTUAL_NETWORK_READY_LIST_FILTER,
                  filter.field('metadata.tenant').equals(tenant),
                ),
              ),
      }}
      autoSelectSingleOption
      isRequired={isRequired}
      createOption={
        onCreate ? { label: t('Create virtual network'), onSelect: onCreate } : undefined
      }
      isDisabled={isDisabled}
      placeholder={t('Select virtual network')}
      loadErrorTitle={t('Could not load virtual networks')}
      onSelectResource={() => {
        void setFieldValue(subnetName, emptyResourceSelectValue());
        void setFieldValue(securityGroupsName, []);
      }}
    />
  );
};

export default VirtualNetworkField;
