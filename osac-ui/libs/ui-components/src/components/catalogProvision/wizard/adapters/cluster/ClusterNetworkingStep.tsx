import { useEffect, useMemo, useRef } from 'react';
import { Alert, Button, FormGroup, FormSection, Stack, StackItem } from '@patternfly/react-core';
import { MultiTypeaheadSelect, type MultiTypeaheadSelectOption } from '@patternfly/react-templates';
import { useField, useFormikContext } from 'formik';

import { type ClusterCatalogItem, Subnets, VirtualNetworks } from '@osac/types';

import type { ClusterWizardValues } from './fields';
import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  resourceDisplayName,
  securityGroupFilterForVirtualNetworkList,
  useSecurityGroups,
  virtualNetworkFilterForSubnetList,
} from '../../../../../api/v1/networking';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { InputField } from '../../../../Form/InputField';
import OsacForm from '../../../../Form/OsacForm';
import {
  ResourceSelectField,
  type ResourceSelectValue,
} from '../../../../Form/ResourceSelectField';
import { emptyResourceSelectValue } from '../../../../Form/resourceSelectValue';
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
  const virtualNetworkId = values.spec.networkAttachment.virtualNetwork.id;

  const definitions = useMemo(() => readCatalogFieldDefinitions(catalogItem), [catalogItem]);
  const podCidrOverlay = useMemo(
    () => getCatalogFieldOverlay('network.pod_cidr', definitions, t('Pod CIDR')),
    [definitions, t],
  );
  const serviceCidrOverlay = useMemo(
    () => getCatalogFieldOverlay('network.service_cidr', definitions, t('Service CIDR')),
    [definitions, t],
  );

  // ── Security groups (manual hooks — no ResourceMultiSelectField exists) ──
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

  const securityGroupOptions = useMemo(
    () =>
      securityGroups.map((group) => ({
        value: group.id,
        label: resourceDisplayName(group.metadata, group.id),
      })),
    [securityGroups],
  );

  const [sgField, , sgHelpers] = useField<ResourceSelectValue[]>(
    'spec.networkAttachment.securityGroups',
  );
  const selectedSgIds = useMemo(
    () => (sgField.value ?? []).map((sg) => sg.id),
    [sgField.value],
  );

  const sgMultiSelectOptions = useMemo<MultiTypeaheadSelectOption[]>(
    () =>
      securityGroupOptions.map((o) => ({
        content: o.label,
        value: o.value,
        selected: selectedSgIds.includes(o.value as string),
      })),
    [securityGroupOptions, selectedSgIds],
  );

  const securityGroupListLoading = Boolean(virtualNetworkId) && securityGroupsLoading;

  // ── Cascade reset: clear subnet and SGs when VN changes ──
  const previousVirtualNetworkIdRef = useRef(virtualNetworkId);
  useEffect(() => {
    const previous = previousVirtualNetworkIdRef.current;
    previousVirtualNetworkIdRef.current = virtualNetworkId;
    if (previous && previous !== virtualNetworkId) {
      void setFieldValue('spec.networkAttachment.subnet', emptyResourceSelectValue());
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
                <ResourceSelectField
                  name="spec.networkAttachment.virtualNetwork"
                  label={t('Virtual network')}
                  fieldId="cluster-virtual-network"
                  service={VirtualNetworks}
                  request={{ filter: VIRTUAL_NETWORK_READY_LIST_FILTER }}
                  autoSelectSingleOption
                  placeholder={t('Select virtual network')}
                  loadErrorTitle={t('Could not load virtual networks')}
                />
                <ResourceSelectField
                  name="spec.networkAttachment.subnet"
                  label={t('Subnet')}
                  fieldId="cluster-subnet"
                  service={Subnets}
                  request={
                    virtualNetworkId
                      ? { filter: virtualNetworkFilterForSubnetList(virtualNetworkId) }
                      : {}
                  }
                  isDisabled={!virtualNetworkId}
                  autoSelectSingleOption
                  placeholder={t('Select subnet')}
                  loadErrorTitle={t('Could not load subnets')}
                />

                {securityGroupsError ? (
                  <Alert
                    variant="danger"
                    isInline
                    title={t('Could not load security groups')}
                  >
                    <Button
                      variant="link"
                      isInline
                      onClick={() => {
                        void refetchSecurityGroups();
                      }}
                    >
                      {t('Retry')}
                    </Button>
                  </Alert>
                ) : null}
                <FormGroup
                  label={t('Security groups')}
                  fieldId="cluster-security-groups"
                >
                  <MultiTypeaheadSelect
                    id="cluster-security-groups"
                    initialOptions={sgMultiSelectOptions}
                    placeholder={
                      securityGroupListLoading
                        ? t('Loading...')
                        : t('Select security groups')
                    }
                    isDisabled={!virtualNetworkId || securityGroupListLoading}
                    noOptionsFoundMessage={(filter) => `No options found for "${filter}"`}
                    onSelectionChange={(_event, selections) => {
                      const newValues: ResourceSelectValue[] = (
                        selections as string[]
                      ).map((id) => {
                        const option = securityGroupOptions.find((o) => o.value === id);
                        return { id, name: option?.label ?? id };
                      });
                      void sgHelpers.setValue(newValues, true);
                      void sgHelpers.setTouched(true);
                    }}
                    toggleProps={{
                      id: 'cluster-security-groups',
                      'aria-label': t('Security groups'),
                      isFullWidth: true,
                      'aria-busy': securityGroupListLoading || undefined,
                    }}
                  />
                </FormGroup>
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
