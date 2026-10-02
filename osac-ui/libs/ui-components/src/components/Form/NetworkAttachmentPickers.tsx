import { useEffect, useMemo, useRef } from 'react';
import { Alert, Button, FormGroup } from '@patternfly/react-core';
import { MultiTypeaheadSelect, type MultiTypeaheadSelectOption } from '@patternfly/react-templates';
import { useField, useFormikContext } from 'formik';

import { Subnets, VirtualNetworks } from '@osac/types';

import { CheckboxField } from './CheckboxField';
import { ResourceSelectField, type ResourceSelectValue } from './ResourceSelectField';
import { emptyResourceSelectValue } from './resourceSelectValue';
import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  resourceDisplayName,
  securityGroupFilterForVirtualNetworkList,
  useSecurityGroups,
  virtualNetworkFilterForSubnetList,
} from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';

interface NetworkAttachmentPickersProps {
  /** Formik field-path prefix, e.g. "spec.networkAttachment". */
  fieldPrefix: string;
  /** HTML id prefix for unique element IDs, e.g. "cluster" or "vm". */
  fieldIdPrefix: string;
  /** Formik field path for the "use default network" checkbox, e.g. "spec.useDefaultNetwork". */
  useDefaultNetworkName: string;
  /** Formik field path for the "auto external IP attachment" checkbox. */
  autoExternalIpName: string;
  /** Helper text displayed below the auto external IP checkbox. */
  autoExternalIpHelperText?: string;
  /**
   * Callback invoked when the user toggles back to the default network.
   * Parent steps can use this to clear wizard validation alerts.
   */
  onResetToDefaults?: () => void;
}

/**
 * Reusable VirtualNetwork → Subnet → SecurityGroups cascade pickers
 * with "use default network" and "auto external IP" checkboxes.
 *
 * Uses ResourceSelectField for VN and subnet (stores ResourceSelectValue
 * with both id and name), and MultiTypeaheadSelect for security groups
 * (stores ResourceSelectValue[]).
 *
 * When the "use default network" checkbox is checked, the VN/subnet pickers
 * are hidden (the backend picks defaults). Security groups are always optional.
 *
 * Cascade behaviour: selecting a new VN resets the subnet and SG fields
 * via the onSelectResource callback.
 */
export const NetworkAttachmentPickers = ({
  fieldPrefix,
  fieldIdPrefix,
  useDefaultNetworkName,
  autoExternalIpName,
  autoExternalIpHelperText,
  onResetToDefaults,
}: NetworkAttachmentPickersProps) => {
  const { t } = useTranslation();
  const { setFieldTouched, validateForm } = useFormikContext();

  const [useDefaultField] = useField<boolean>(useDefaultNetworkName);
  const useDefaultNetwork = useDefaultField.value;

  const [vnField] = useField<ResourceSelectValue>(`${fieldPrefix}.virtualNetwork`);
  const [, , subnetHelpers] = useField<ResourceSelectValue>(`${fieldPrefix}.subnet`);
  const [sgField, , sgHelpers] = useField<ResourceSelectValue[]>(`${fieldPrefix}.securityGroups`);

  const virtualNetworkId = vnField.value?.id ?? '';

  // ── Clear validation when toggling back to defaults ──
  const previousUseDefaultRef = useRef(useDefaultNetwork);
  useEffect(() => {
    const wasCustom = previousUseDefaultRef.current === false;
    previousUseDefaultRef.current = useDefaultNetwork;

    if (!useDefaultNetwork || !wasCustom) {
      return;
    }

    void setFieldTouched(`${fieldPrefix}.virtualNetwork`, false, false);
    void setFieldTouched(`${fieldPrefix}.subnet`, false, false);
    void setFieldTouched(`${fieldPrefix}.securityGroups`, false, false);
    onResetToDefaults?.();
    void validateForm();
  }, [fieldPrefix, onResetToDefaults, setFieldTouched, useDefaultNetwork, validateForm]);

  // ── Security groups (manual hooks — no ResourceMultiSelectField exists) ──
  const securityGroupFilter = virtualNetworkId
    ? securityGroupFilterForVirtualNetworkList(virtualNetworkId)
    : undefined;
  const {
    data: securityGroups = [],
    isLoading: securityGroupsLoading,
    error: securityGroupsError,
    refetch: refetchSecurityGroups,
  } = useSecurityGroups(securityGroupFilter ? { filter: securityGroupFilter } : {}, {
    enabled: Boolean(virtualNetworkId),
  });

  const securityGroupOptions = useMemo(
    () =>
      securityGroups.map((group) => ({
        value: group.id,
        label: resourceDisplayName(group.metadata, group.id),
      })),
    [securityGroups],
  );

  const selectedSgIds = useMemo(() => (sgField.value ?? []).map((sg) => sg.id), [sgField.value]);

  const sgMultiSelectOptions = useMemo<MultiTypeaheadSelectOption[]>(
    () =>
      securityGroupOptions.map((o) => ({
        content: o.label,
        value: o.value,
        selected: selectedSgIds.includes(String(o.value)),
      })),
    [securityGroupOptions, selectedSgIds],
  );

  const securityGroupListLoading = Boolean(virtualNetworkId) && securityGroupsLoading;

  return (
    <>
      <CheckboxField
        name={useDefaultNetworkName}
        label={t('Use tenant default network')}
        fieldId={`${fieldIdPrefix}-use-default-network`}
      />
      {!useDefaultNetwork && (
        <>
          <ResourceSelectField
            name={`${fieldPrefix}.virtualNetwork`}
            label={t('Virtual network')}
            fieldId={`${fieldIdPrefix}-virtual-network`}
            service={VirtualNetworks}
            request={{ filter: VIRTUAL_NETWORK_READY_LIST_FILTER }}
            autoSelectSingleOption
            placeholder={t('Select virtual network')}
            loadErrorTitle={t('Could not load virtual networks')}
            onSelectResource={() => {
              void subnetHelpers.setValue(emptyResourceSelectValue());
              void sgHelpers.setValue([]);
            }}
          />
          <ResourceSelectField
            name={`${fieldPrefix}.subnet`}
            label={t('Subnet')}
            fieldId={`${fieldIdPrefix}-subnet`}
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
            <Alert variant="danger" isInline title={t('Could not load security groups')}>
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
          <FormGroup label={t('Security groups')} fieldId={`${fieldIdPrefix}-security-groups`}>
            <MultiTypeaheadSelect
              id={`${fieldIdPrefix}-security-groups`}
              initialOptions={sgMultiSelectOptions}
              placeholder={securityGroupListLoading ? t('Loading...') : t('Select security groups')}
              isDisabled={!virtualNetworkId || securityGroupListLoading}
              noOptionsFoundMessage={(filter) => `No options found for "${filter}"`}
              onSelectionChange={(_event, selections) => {
                const newValues: ResourceSelectValue[] = (selections as string[]).map((id) => {
                  const option = securityGroupOptions.find((o) => o.value === id);
                  return { id, name: option?.label ?? id };
                });
                void sgHelpers.setValue(newValues, true);
                void sgHelpers.setTouched(true);
              }}
              toggleProps={{
                id: `${fieldIdPrefix}-security-groups`,
                'aria-label': t('Security groups'),
                isFullWidth: true,
                'aria-busy': securityGroupListLoading || undefined,
              }}
            />
          </FormGroup>
        </>
      )}
      <CheckboxField
        name={autoExternalIpName}
        label={t('Auto External IP Attachment')}
        fieldId={`${fieldIdPrefix}-auto-external-ip`}
        helperText={autoExternalIpHelperText}
      />
    </>
  );
};
