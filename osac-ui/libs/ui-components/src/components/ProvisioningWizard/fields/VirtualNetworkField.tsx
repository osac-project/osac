import { useFormikContext } from 'formik';

import { VirtualNetworks } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { VIRTUAL_NETWORK_READY_LIST_FILTER } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceSelectField } from '../../Form/ResourceSelectField';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

interface VirtualNetworkFieldProps extends ProvisioningFieldControlProps {
  subnetName: string;
  securityGroupsName: string;
  isRequired?: boolean;
  showDescription?: boolean;
  onCreate?: () => void;
}

const VirtualNetworkField = ({
  name,
  subnetName,
  securityGroupsName,
  isRequired = false,
  showDescription = false,
  onCreate,
  isDisabled = false,
}: VirtualNetworkFieldProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();

  return (
    <ResourceSelectField
      name={name}
      label={t('Virtual network')}
      fieldId={name}
      service={VirtualNetworks}
      request={{ filter: VIRTUAL_NETWORK_READY_LIST_FILTER }}
      autoSelectSingleOption
      isRequired={isRequired}
      showDescription={showDescription}
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
