import { useNavigate } from 'react-router-dom';
import {
  Alert,
  Button,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import * as Yup from 'yup';

import type { NATGateway, VirtualNetwork } from '@osac/types';

import { natGatewayCreatePath } from './natGatewayPaths';
import { isDefaultResource } from './resourceOrigin';
import { useNetworkingTenants } from './useNetworkingTenants';
import { isVirtualNetworkReady, virtualNetworkOptionLabel } from './virtualNetworkReadiness';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import OsacForm from '../Form/OsacForm';
import { SelectField } from '../Form/SelectField';

interface NatGatewayCreateModalProps {
  virtualNetworks: VirtualNetwork[];
  natGateways: NATGateway[];
  isLoading?: boolean;
  error?: unknown;
  /** Pre-selects this virtual network. */
  virtualNetworkId?: string;
  onClose: () => void;
}

/**
 * Picks the virtual network of a new NAT gateway, then continues to the attach
 * wizard. Only READY, non-default networks without a NAT gateway are offered.
 */
const NatGatewayCreateModal = ({
  virtualNetworks,
  natGateways,
  isLoading = false,
  error,
  virtualNetworkId = '',
  onClose,
}: NatGatewayCreateModalProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();
  const navigate = useNavigate();
  const withNatGateway = new Set(natGateways.map((nat) => nat.spec?.virtualNetwork?.id));
  const options = virtualNetworks
    .filter(
      (vn) => isVirtualNetworkReady(vn) && !isDefaultResource(vn) && !withNatGateway.has(vn.id),
    )
    .map((vn) => ({
      value: vn.id,
      label: virtualNetworkOptionLabel(vn, isAdmin ? tenantName : undefined),
    }));
  const initialVirtualNetwork = options.some((option) => option.value === virtualNetworkId)
    ? virtualNetworkId
    : '';
  const noneAvailable = !isLoading && !error && options.length === 0;

  return (
    <Formik
      initialValues={{ virtualNetwork: initialVirtualNetwork }}
      validationSchema={Yup.object({
        virtualNetwork: Yup.string().required(t('Virtual network is required')),
      })}
      onSubmit={(values) => navigate(natGatewayCreatePath(values.virtualNetwork))}
    >
      {({ submitForm }) => (
        <Modal
          variant="small"
          isOpen
          onClose={onClose}
          aria-labelledby="nat-gateway-create-modal-title"
        >
          <ModalHeader title={t('Create NAT gateway')} labelId="nat-gateway-create-modal-title" />
          <ModalBody>
            <Stack hasGutter>
              {error ? (
                <StackItem>
                  <Alert variant="danger" title={t('Error loading virtual networks')} isInline>
                    {getErrorMessage(error)}
                  </Alert>
                </StackItem>
              ) : null}
              {noneAvailable ? (
                <StackItem>
                  <Alert
                    variant="warning"
                    title={t('No virtual network can take a NAT gateway')}
                    isInline
                  >
                    {t(
                      'A NAT gateway needs a ready virtual network that is not a tenant default and has no NAT gateway yet.',
                    )}
                  </Alert>
                </StackItem>
              ) : null}
              <StackItem>
                <OsacForm>
                  <SelectField
                    name="virtualNetwork"
                    label={t('Virtual network')}
                    fieldId="nat-gateway-virtual-network"
                    isRequired
                    isLoading={isLoading}
                    isDisabled={noneAvailable}
                    placeholder={t('Select a virtual network')}
                    helperText={t(
                      'Ready virtual networks that are not a tenant default and have no NAT gateway.',
                    )}
                    options={options}
                  />
                </OsacForm>
              </StackItem>
            </Stack>
          </ModalBody>
          <ModalFooter>
            <Button variant="primary" onClick={submitForm} isDisabled={noneAvailable}>
              {t('Continue')}
            </Button>
            <Button variant="link" onClick={onClose}>
              {t('Cancel')}
            </Button>
          </ModalFooter>
        </Modal>
      )}
    </Formik>
  );
};

export default NatGatewayCreateModal;
