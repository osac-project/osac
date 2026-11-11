import { useMemo } from 'react';
import {
  Alert,
  Button,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Stack,
  StackItem,
  Tooltip,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import * as Yup from 'yup';

import { SubnetState, type VirtualNetwork } from '@osac/types';
import {
  useCreateSubnet,
  useSubnets,
  useVirtualNetworks,
} from '@osac/ui-components/api/v1/networking';

import { isDefaultResource } from './resourceOrigin';
import { subnetCidrExample } from './subnetCidrExample';
import { useNetworkingTenants } from './useNetworkingTenants';
import { isVirtualNetworkReady, virtualNetworkOptionLabel } from './virtualNetworkReadiness';
import TextField from '../../components/Form/base/TextField';
import OsacForm from '../../components/Form/OsacForm';
import { SelectField } from '../../components/Form/SelectField';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import {
  buildCidrSchema,
  hasSubnetOverlap,
  isSubnetWithinVN,
} from '../../validation/cidr-validation';
import NameField from '../Form/NameField';

interface SubnetCreateModalProps {
  onClose: () => void;
  /** Pre-selects this virtual network. */
  virtualNetworkId?: string;
  /** Locks the pre-selected virtual network, e.g. on the virtual network's page. */
  lockVirtualNetwork?: boolean;
}

interface SubnetCreateFormValues {
  metadata: { name: string };
  virtualNetwork: string;
  ipv4Cidr: string;
}

export const SubnetCreateModal = ({
  onClose,
  virtualNetworkId = '',
  lockVirtualNetwork = false,
}: SubnetCreateModalProps) => {
  const { mutateAsync: create, error } = useCreateSubnet();
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();
  const { role } = useSession();
  const {
    data: virtualNetworks = [],
    isLoading: isLoadingVirtualNetworks,
    error: virtualNetworksError,
  } = useVirtualNetworks();
  const { data: subnets = [], isLoading: isLoadingSubnets, error: subnetsError } = useSubnets();

  // Subnets can only be created in a READY virtual network.
  const selectableNetworks = useMemo(
    () =>
      virtualNetworks.filter(
        (vn) => isVirtualNetworkReady(vn) || (lockVirtualNetwork && vn.id === virtualNetworkId),
      ),
    [lockVirtualNetwork, virtualNetworkId, virtualNetworks],
  );
  // A network from the list filter is preselected only when it can be picked.
  const initialVirtualNetwork = selectableNetworks.some((vn) => vn.id === virtualNetworkId)
    ? virtualNetworkId
    : '';
  const findNetwork = (id: string): VirtualNetwork | undefined =>
    virtualNetworks.find((vn) => vn.id === id);
  // In a tenant default network, the server accepts another subnet only once the default
  // subnet is READY. While the subnets load or fail to load, the server decides.
  const defaultSubnetNotReadyReason = (vnId: string): string | undefined => {
    if (isLoadingSubnets || subnetsError || !isDefaultResource(findNetwork(vnId))) {
      return undefined;
    }
    const defaultSubnetReady = subnets.some(
      (s) =>
        s.spec?.virtualNetwork?.id === vnId &&
        isDefaultResource(s) &&
        s.status?.state === SubnetState.READY,
    );
    return defaultSubnetReady ? undefined : t('Available when the default subnet is Ready.');
  };
  const parentIdOf = (parent: unknown): string =>
    String((parent as Partial<SubnetCreateFormValues>).virtualNetwork ?? '');

  const validationSchema = Yup.object({
    metadata: Yup.object({
      name: Yup.string().required(t('Name is required')),
    }),
    virtualNetwork: Yup.string().required(t('Virtual network is required')),
    ipv4Cidr: buildCidrSchema(t, 'ipv4')
      .required(t('IPv4 CIDR is required'))
      .test('within-vn', t('CIDR must be within parent virtual network range'), function (value) {
        const parentCidr = findNetwork(parentIdOf(this.parent))?.spec?.ipv4Cidr;
        if (!value || !parentCidr) {
          return true;
        }
        return isSubnetWithinVN(value, parentCidr);
      })
      .test('no-overlap', function (value) {
        if (!value) {
          return true;
        }
        const parentId = parentIdOf(this.parent);
        const overlappingSubnet = subnets.find((s) => {
          const existingCidr = s.spec?.ipv4Cidr;
          return (
            s.spec?.virtualNetwork?.id === parentId &&
            existingCidr &&
            hasSubnetOverlap(value, [existingCidr])
          );
        });
        if (overlappingSubnet) {
          return this.createError({
            message: t('CIDR overlaps with existing subnet "{{name}}" ({{cidr}})', {
              name: overlappingSubnet.metadata?.name || overlappingSubnet.id,
              cidr: overlappingSubnet.spec?.ipv4Cidr,
            }),
          });
        }
        return true;
      }),
  });

  return (
    <Formik<SubnetCreateFormValues>
      enableReinitialize
      initialValues={{
        metadata: { name: '' },
        virtualNetwork: initialVirtualNetwork,
        ipv4Cidr: '',
      }}
      validationSchema={validationSchema}
      onSubmit={async (values) => {
        const parent = findNetwork(values.virtualNetwork);
        try {
          await create({
            metadata: {
              name: values.metadata.name,
              project: parent?.metadata?.project,
              // A provider admin creates the subnet in the virtual network's tenant.
              ...(role === 'admin' && parent?.metadata?.tenant
                ? { tenant: parent.metadata.tenant }
                : {}),
            },
            spec: {
              virtualNetwork: { id: values.virtualNetwork },
              ipv4Cidr: values.ipv4Cidr,
            },
          });
          onClose();
        } catch {
          // tanstack handles the error
        }
      }}
    >
      {({ values, submitForm, isSubmitting }) => {
        const parentCidr = findNetwork(values.virtualNetwork)?.spec?.ipv4Cidr;
        // The example skips ranges the network's subnets already use.
        const usedCidrs = subnets
          .filter((s) => s.spec?.virtualNetwork?.id === values.virtualNetwork)
          .map((s) => s.spec?.ipv4Cidr ?? '');
        const createDisabledReason = defaultSubnetNotReadyReason(values.virtualNetwork);
        const createButton = (
          <Button
            variant="primary"
            onClick={createDisabledReason ? undefined : submitForm}
            isDisabled={isSubmitting}
            isAriaDisabled={Boolean(createDisabledReason)}
            isLoading={isSubmitting}
          >
            {t('Create')}
          </Button>
        );
        return (
          <Modal
            variant="small"
            isOpen
            onClose={isSubmitting ? undefined : onClose}
            aria-labelledby="subnet-create-modal-title"
          >
            <ModalHeader title={t('Create subnet')} labelId="subnet-create-modal-title" />
            <ModalBody>
              <Stack hasGutter>
                {virtualNetworksError ? (
                  <StackItem>
                    <Alert variant="danger" title={t('Error loading virtual networks')} isInline>
                      {getErrorMessage(virtualNetworksError)}
                    </Alert>
                  </StackItem>
                ) : null}
                <StackItem>
                  <OsacForm>
                    <SelectField
                      name="virtualNetwork"
                      label={t('Virtual network')}
                      fieldId="subnet-virtual-network"
                      isRequired
                      isLoading={isLoadingVirtualNetworks}
                      isDisabled={lockVirtualNetwork}
                      placeholder={t('Select a virtual network')}
                      helperText={
                        lockVirtualNetwork
                          ? undefined
                          : t('Only ready virtual networks are listed.')
                      }
                      options={selectableNetworks.map((vn) => ({
                        value: vn.id,
                        label: virtualNetworkOptionLabel(vn, isAdmin ? tenantName : undefined),
                      }))}
                    />
                    <NameField />
                    <TextField
                      name="ipv4Cidr"
                      label={t('IPv4 CIDR')}
                      fieldId="subnet-ipv4-cidr"
                      isRequired
                      placeholder={subnetCidrExample(parentCidr, usedCidrs)}
                      helperText={
                        parentCidr
                          ? t('Must be inside {{cidr}}.', { cidr: parentCidr })
                          : t('Must be inside the virtual network CIDR.')
                      }
                    />
                  </OsacForm>
                </StackItem>
                {createDisabledReason ? (
                  <StackItem>
                    <Alert variant="info" title={createDisabledReason} isInline isPlain />
                  </StackItem>
                ) : null}
                {!!error && (
                  <StackItem>
                    <Alert variant="danger" title={t('Failed to create subnet')} isInline>
                      {getErrorMessage(error)}
                    </Alert>
                  </StackItem>
                )}
              </Stack>
            </ModalBody>
            <ModalFooter>
              {createDisabledReason ? (
                <Tooltip content={createDisabledReason}>{createButton}</Tooltip>
              ) : (
                createButton
              )}
              <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
                {t('Cancel')}
              </Button>
            </ModalFooter>
          </Modal>
        );
      }}
    </Formik>
  );
};
