import { useEffect, useMemo, useRef } from 'react';
import { Alert, Button, FormGroup } from '@patternfly/react-core';
import { MultiTypeaheadSelect, type MultiTypeaheadSelectOption } from '@patternfly/react-templates';
import { useField, useFormikContext } from 'formik';

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

export interface NetworkPickerFieldsProps {
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
 * Cascade behaviour: selecting a new VN resets the subnet and SG fields.
 * Subnet and SG queries are scoped to the selected VN.
 */
export const NetworkPickerFields = ({ fieldPrefix, fieldIdPrefix }: NetworkPickerFieldsProps) => {
  const { t } = useTranslation();
  const { values, setFieldValue } = useFormikContext<Record<string, unknown>>();

  // Read the current VN id from the form values.
  const virtualNetworkId =
    (getNestedValue(values, `${fieldPrefix}.virtualNetwork`) as ResourceSelectValue | undefined)
      ?.id ?? '';

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

  const [sgField, , sgHelpers] = useField<ResourceSelectValue[]>(`${fieldPrefix}.securityGroups`);
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

  // ── Cascade reset: clear subnet and SGs when VN changes ──
  const previousVirtualNetworkIdRef = useRef(virtualNetworkId);
  useEffect(() => {
    const previous = previousVirtualNetworkIdRef.current;
    previousVirtualNetworkIdRef.current = virtualNetworkId;
    if (previous && previous !== virtualNetworkId) {
      void setFieldValue(`${fieldPrefix}.subnet`, emptyResourceSelectValue());
      void setFieldValue(`${fieldPrefix}.securityGroups`, []);
    }
  }, [fieldPrefix, setFieldValue, virtualNetworkId]);

  return (
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
      />
      <ResourceSelectField
        name={`${fieldPrefix}.subnet`}
        label={t('Subnet')}
        fieldId={`${fieldIdPrefix}-subnet`}
        service={Subnets}
        request={
          virtualNetworkId ? { filter: virtualNetworkFilterForSubnetList(virtualNetworkId) } : {}
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
  );
};

/** Resolve a dot-separated path against a nested object. */
const getNestedValue = (obj: Record<string, unknown>, path: string): unknown =>
  path.split('.').reduce<unknown>((current, key) => {
    if (current !== null && typeof current === 'object') {
      return (current as Record<string, unknown>)[key];
    }
    return undefined;
  }, obj);
