import { useEffect } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useField, useFormikContext } from 'formik';

import { useSubnet } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface PopulateVirtualNetworkFromSubnetProps {
  virtualNetworkName: string;
  subnetName: string;
}

/**
 * When a catalog item supplies only a subnet, fetch its parent virtual network
 * and populate the form so dependent network fields can load.
 */
const PopulateVirtualNetworkFromSubnet = ({
  virtualNetworkName,
  subnetName,
}: PopulateVirtualNetworkFromSubnetProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();
  const [subnetField] = useField<ResourceSelectValue>(subnetName);
  const [virtualNetworkField] = useField<ResourceSelectValue>(virtualNetworkName);
  const subnetId = subnetField.value?.id ?? '';
  const virtualNetworkId = virtualNetworkField.value?.id ?? '';
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

export default PopulateVirtualNetworkFromSubnet;
