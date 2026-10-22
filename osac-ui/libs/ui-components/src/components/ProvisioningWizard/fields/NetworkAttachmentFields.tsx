import { useEffect } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useField, useFormikContext } from 'formik';

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

interface NetworkAttachmentFieldsProps {
  name: string;
  isDisabled?: boolean;
}

const NetworkAttachmentFields = ({ name, isDisabled = false }: NetworkAttachmentFieldsProps) => {
  const [{ value: useDefaultNetwork }] = useField<boolean>(`${name}.useDefaultNetwork`);

  return (
    <>
      <UseDefaultNetworkField name={`${name}.useDefaultNetwork`} isDisabled={isDisabled} />
      {useDefaultNetwork ? null : (
        <>
          <CatalogSubnetVirtualNetworkHydrator
            virtualNetworkName={`${name}.virtualNetwork`}
            subnetName={`${name}.subnet`}
          />
          <VirtualNetworkField
            name={`${name}.virtualNetwork`}
            subnetName={`${name}.subnet`}
            securityGroupsName={`${name}.securityGroups`}
            isDisabled={isDisabled}
          />
          <SubnetField
            name={`${name}.subnet`}
            virtualNetworkName={`${name}.virtualNetwork`}
            isDisabled={isDisabled}
          />
          <SecurityGroupsField
            name={`${name}.securityGroups`}
            virtualNetworkName={`${name}.virtualNetwork`}
            isDisabled={isDisabled}
          />
        </>
      )}
    </>
  );
};

export default NetworkAttachmentFields;
