import { useEffect, useMemo, useRef } from 'react';
import { Alert, Button, FormSection, Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { ClusterCatalogItem } from '@osac/types';

import type { ClusterWizardValues } from './fields';
import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  resourceDisplayName,
  securityGroupFilterForVirtualNetworkList,
  useSecurityGroups,
  useSubnets,
  useVirtualNetworks,
  virtualNetworkFilterForSubnetList,
} from '../../../../../api/v1/networking';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { InputField } from '../../../../Form/InputField';
import { MultiSelectField } from '../../../../Form/MultiSelectField';
import OsacForm from '../../../../Form/OsacForm';
import { SelectField } from '../../../../Form/SelectField';
import { SwitchField } from '../../../../Form/SwitchField';
import { getCatalogFieldOverlay, readCatalogFieldDefinitions } from '../../catalogOverlay';
import { useWizardValidation } from '../../WizardValidationContext';

interface Props {
  catalogItem: ClusterCatalogItem | null;
}

export const ClusterNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { clearValidationAlert } = useWizardValidation();
  const { values, setFieldValue, setFieldTouched, validateForm } =
    useFormikContext<ClusterWizardValues>();

  const useDefaultNetwork = values.spec.useDefaultNetwork;
  const virtualNetworkId = values.spec.networkAttachment.virtualNetwork;

  const definitions = useMemo(() => readCatalogFieldDefinitions(catalogItem), [catalogItem]);
  const podCidrOverlay = useMemo(
    () => getCatalogFieldOverlay('network.pod_cidr', definitions, t('Pod CIDR')),
    [definitions, t],
  );
  const serviceCidrOverlay = useMemo(
    () => getCatalogFieldOverlay('network.service_cidr', definitions, t('Service CIDR')),
    [definitions, t],
  );

  const {
    data: virtualNetworks = [],
    isPending: virtualNetworksLoading,
    isError: virtualNetworksError,
    refetch: refetchVirtualNetworks,
  } = useVirtualNetworks(
    { filter: VIRTUAL_NETWORK_READY_LIST_FILTER },
    { enabled: !useDefaultNetwork },
  );

  const subnetFilter = virtualNetworkId
    ? virtualNetworkFilterForSubnetList(virtualNetworkId)
    : undefined;
  const {
    data: subnets = [],
    isPending: subnetsLoading,
    isError: subnetsError,
    refetch: refetchSubnets,
  } = useSubnets(subnetFilter ? { filter: subnetFilter } : {}, {
    enabled: !useDefaultNetwork && Boolean(virtualNetworkId),
  });

  const securityGroupFilter = virtualNetworkId
    ? securityGroupFilterForVirtualNetworkList(virtualNetworkId)
    : undefined;
  const {
    data: securityGroups = [],
    isPending: securityGroupsLoading,
    isError: securityGroupsError,
    refetch: refetchSecurityGroups,
  } = useSecurityGroups(securityGroupFilter ? { filter: securityGroupFilter } : {}, {
    enabled: !useDefaultNetwork && Boolean(virtualNetworkId),
  });

  const virtualNetworkOptions = useMemo(
    () =>
      virtualNetworks.map((vn) => ({
        value: vn.id,
        label: resourceDisplayName(vn.metadata, vn.id),
      })),
    [virtualNetworks],
  );

  const subnetOptions = useMemo(
    () =>
      subnets.map((subnet) => ({
        value: subnet.id,
        label: resourceDisplayName(subnet.metadata, subnet.id),
      })),
    [subnets],
  );

  const securityGroupOptions = useMemo(
    () =>
      securityGroups.map((group) => ({
        value: group.id,
        label: resourceDisplayName(group.metadata, group.id),
      })),
    [securityGroups],
  );

  // Cascade reset: clear subnet and SGs when VN changes
  const previousVirtualNetworkIdRef = useRef(virtualNetworkId);
  useEffect(() => {
    const previous = previousVirtualNetworkIdRef.current;
    previousVirtualNetworkIdRef.current = virtualNetworkId;
    if (previous && previous !== virtualNetworkId) {
      void setFieldValue('spec.networkAttachment.subnet', '');
      void setFieldValue('spec.networkAttachment.securityGroups', []);
    }
  }, [setFieldValue, virtualNetworkId]);

  // Clear validation alerts when toggling back to defaults
  const previousUseDefaultRef = useRef(useDefaultNetwork);
  useEffect(() => {
    const wasCustom = previousUseDefaultRef.current === false;
    previousUseDefaultRef.current = useDefaultNetwork;

    if (!useDefaultNetwork || !wasCustom) {
      return;
    }

    void setFieldTouched('spec.networkAttachment.virtualNetwork', false, false);
    void setFieldTouched('spec.networkAttachment.subnet', false, false);
    void setFieldTouched('spec.networkAttachment.securityGroups', false, false);
    clearValidationAlert();
    void validateForm();
  }, [clearValidationAlert, setFieldTouched, useDefaultNetwork, validateForm]);

  if (!catalogItem) {
    return null;
  }

  const listError = virtualNetworksError || subnetsError || securityGroupsError;
  const loadingPlaceholder = t('Loading...');
  const subnetListLoading = Boolean(virtualNetworkId) && subnetsLoading;
  const securityGroupListLoading = Boolean(virtualNetworkId) && securityGroupsLoading;

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <FormSection title={t('Infrastructure Networking')} titleElement="h2">
            <SwitchField
              name="spec.useDefaultNetwork"
              label={t('Use tenant default network')}
              fieldId="cluster-use-default-network"
              isReversed
            />
            {!useDefaultNetwork && (
              <>
                {listError ? (
                  <Alert variant="danger" isInline title={t('Could not load networking resources')}>
                    <Button
                      variant="link"
                      isInline
                      onClick={() => {
                        void refetchVirtualNetworks();
                        void refetchSubnets();
                        void refetchSecurityGroups();
                      }}
                    >
                      {t('Retry')}
                    </Button>
                  </Alert>
                ) : null}
                <SelectField
                  name="spec.networkAttachment.virtualNetwork"
                  label={t('Virtual network')}
                  fieldId="cluster-virtual-network"
                  autoSelectSingleOption
                  isLoading={virtualNetworksLoading}
                  loadingPlaceholder={loadingPlaceholder}
                  placeholder={t('Select virtual network')}
                  options={virtualNetworkOptions}
                />
                <SelectField
                  name="spec.networkAttachment.subnet"
                  label={t('Subnet')}
                  fieldId="cluster-subnet"
                  autoSelectSingleOption
                  isLoading={subnetListLoading}
                  isDisabled={!virtualNetworkId}
                  loadingPlaceholder={loadingPlaceholder}
                  placeholder={t('Select subnet')}
                  options={subnetOptions}
                />
                <MultiSelectField
                  name="spec.networkAttachment.securityGroups"
                  label={t('Security groups')}
                  fieldId="cluster-security-groups"
                  autoSelectSingleOption
                  isLoading={securityGroupListLoading}
                  isDisabled={!virtualNetworkId}
                  loadingPlaceholder={loadingPlaceholder}
                  placeholder={t('Select security groups')}
                  options={securityGroupOptions}
                />
              </>
            )}
            <SwitchField
              name="spec.autoExternalIpAttachment"
              label={t('Auto External IP Attachment')}
              fieldId="cluster-auto-external-ip"
              helperText={t(
                'Automatically provision external IPs for the cluster API and ingress endpoints.',
              )}
              isReversed
            />
          </FormSection>
          <FormSection title={t('Cluster Networking')} titleElement="h2">
            <InputField
              name="spec.network.podCidr"
              label={podCidrOverlay.label}
              fieldId="cluster-pod-cidr"
              isDisabled={!podCidrOverlay.editable}
              helperText={t('Use IPv4 CIDR notation (for example 10.128.0.0/14).')}
            />
            <InputField
              name="spec.network.serviceCidr"
              label={serviceCidrOverlay.label}
              fieldId="cluster-service-cidr"
              isDisabled={!serviceCidrOverlay.editable}
              helperText={t('Use IPv4 CIDR notation (for example 172.30.0.0/16).')}
            />
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
