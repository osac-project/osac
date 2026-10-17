import { useEffect } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useField, useFormikContext } from 'formik';

import AutoExternalIpField from './AutoExternalIpField';
import ProvisioningField from './ProvisioningField';
import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import SecurityGroupsField from './SecurityGroupsField';
import SubnetField from './SubnetField';
import UseDefaultNetworkField from './UseDefaultNetworkField';
import VirtualNetworkField from './VirtualNetworkField';
import { useSubnet } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

const CatalogSubnetVirtualNetworkHydrator = ({
  virtualNetworkName,
  subnetName,
}: {
  virtualNetworkName: string;
  subnetName: string;
}) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();
  const [subnetField] = useField<ResourceSelectValue>(subnetName);
  const [virtualNetworkField] = useField<ResourceSelectValue>(virtualNetworkName);
  const subnetId = subnetField.value?.id?.trim() ?? '';
  const virtualNetworkId = virtualNetworkField.value?.id?.trim() ?? '';
  const { data: subnet, error, refetch } = useSubnet(virtualNetworkId ? '' : subnetId);

  useEffect(() => {
    const reference = subnet?.spec?.virtualNetwork;
    if (!reference?.id || virtualNetworkId) {
      return;
    }
    void setFieldValue(virtualNetworkName, { id: reference.id, name: reference.name || '' }, false);
  }, [setFieldValue, subnet?.spec?.virtualNetwork, virtualNetworkId, virtualNetworkName]);

  if (!error) {
    return null;
  }

  return (
    <Alert variant="danger" isInline title={t('Could not load subnet')}>
      <Stack hasGutter>
        <StackItem>{getErrorMessage(error)}</StackItem>
        <StackItem>
          <Button variant="link" isInline onClick={() => void refetch()}>
            {t('Retry')}
          </Button>
        </StackItem>
      </Stack>
    </Alert>
  );
};

interface NetworkAttachmentFieldsProps extends ProvisioningFieldControlProps {
  useDefaultNetworkName: string;
  virtualNetworkName: string;
  subnetName: string;
  securityGroupsName: string;
  autoExternalIpName: string;
  fieldIdPrefix: string;
  autoExternalIpHelperText?: string;
}

const NetworkAttachmentFields = ({
  useDefaultNetworkName,
  virtualNetworkName,
  subnetName,
  securityGroupsName,
  autoExternalIpName,
  fieldIdPrefix,
  autoExternalIpHelperText,
  isDisabled = false,
}: NetworkAttachmentFieldsProps) => {
  const [{ value: useDefaultNetwork }] = useField<boolean>(useDefaultNetworkName);

  return (
    <>
      <UseDefaultNetworkField
        name={useDefaultNetworkName}
        fieldId={`${fieldIdPrefix}-use-default-network`}
        isDisabled={isDisabled}
      />
      {useDefaultNetwork ? null : (
        <>
          <CatalogSubnetVirtualNetworkHydrator
            virtualNetworkName={virtualNetworkName}
            subnetName={subnetName}
          />
          <VirtualNetworkField
            name={virtualNetworkName}
            fieldId={`${fieldIdPrefix}-virtual-network`}
            subnetName={subnetName}
            securityGroupsName={securityGroupsName}
            isDisabled={isDisabled}
          />
          <SubnetField
            name={subnetName}
            fieldId={`${fieldIdPrefix}-subnet`}
            virtualNetworkName={virtualNetworkName}
            isDisabled={isDisabled}
          />
          <SecurityGroupsField
            name={securityGroupsName}
            fieldId={`${fieldIdPrefix}-security-groups`}
            virtualNetworkName={virtualNetworkName}
            isDisabled={isDisabled}
          />
        </>
      )}
      <ProvisioningField name={autoExternalIpName}>
        {({ valueName, isDisabled: autoExternalIpDisabled }) => (
          <AutoExternalIpField
            name={valueName}
            fieldId={`${fieldIdPrefix}-auto-external-ip`}
            helperText={autoExternalIpHelperText}
            isDisabled={autoExternalIpDisabled}
          />
        )}
      </ProvisioningField>
    </>
  );
};

export default NetworkAttachmentFields;
