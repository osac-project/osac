import { useMemo } from 'react';
import { Alert, Button, FormGroup } from '@patternfly/react-core';
import { MultiTypeaheadSelect, type MultiTypeaheadSelectOption } from '@patternfly/react-templates';
import { useField } from 'formik';

import { Subnets, VirtualNetworks } from '@osac/types';

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
}

/**
 * Reusable VirtualNetwork → Subnet → SecurityGroups cascade pickers.
 *
 * Uses ResourceSelectField for VN and subnet (stores ResourceSelectValue
 * with both id and name), and MultiTypeaheadSelect for security groups
 * (stores ResourceSelectValue[]).
 *
 * Cascade behaviour: selecting a new VN resets the subnet and SG fields
 * via the onSelectResource callback.
 */
export const NetworkAttachmentPickers = ({
  fieldPrefix,
  fieldIdPrefix,
}: NetworkAttachmentPickersProps) => {
  const { t } = useTranslation();

  const [vnField] = useField<ResourceSelectValue>(`${fieldPrefix}.virtualNetwork`);
  const [, , subnetHelpers] = useField<ResourceSelectValue>(`${fieldPrefix}.subnet`);
  const [sgField, , sgHelpers] = useField<ResourceSelectValue[]>(`${fieldPrefix}.securityGroups`);

  const virtualNetworkId = vnField.value?.id ?? '';

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
      <ResourceSelectField
        name={`${fieldPrefix}.virtualNetwork`}
        label={t('Virtual network')}
        fieldId={`${fieldIdPrefix}-virtual-network`}
        service={VirtualNetworks}
        request={{ filter: VIRTUAL_NETWORK_READY_LIST_FILTER }}
        isRequired
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
          virtualNetworkId ? { filter: virtualNetworkFilterForSubnetList(virtualNetworkId) } : {}
        }
        isRequired
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
  );
};
