import { type MouseEvent, type Ref, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  FormGroup,
  MenuToggle,
  type MenuToggleElement,
  Select,
  SelectList,
  SelectOption,
} from '@patternfly/react-core';
import { MultiTypeaheadSelect, type MultiTypeaheadSelectOption } from '@patternfly/react-templates';
import { useField } from 'formik';

import { SecurityGroups } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useListResource } from '../../../api/use-resource';
import { securityGroupFilterForVirtualNetworkList } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { getVisibleFieldError } from '../../Form/fieldError';
import { useShowFieldValidationErrors } from '../../Form/FieldValidationContext';
import { FormFieldHelper } from '../../Form/FormFieldHelper';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface SecurityGroupsFieldProps extends ProvisioningFieldControlProps {
  virtualNetworkName: string;
  isRequired?: boolean;
  showDescription?: boolean;
  isSingleSelection?: boolean;
}

const SecurityGroupsField = ({
  name,
  virtualNetworkName,
  isRequired = false,
  showDescription = false,
  isSingleSelection = false,
  isDisabled = false,
}: SecurityGroupsFieldProps) => {
  const { t } = useTranslation();
  const [isSingleSelectOpen, setIsSingleSelectOpen] = useState(false);
  const [virtualNetwork] = useField<ResourceSelectValue>(virtualNetworkName);
  const [sgField, sgMeta, sgHelpers] = useField<ResourceSelectValue[]>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(sgMeta, showValidationErrors);
  const virtualNetworkId = virtualNetwork.value?.id ?? '';

  const securityGroupFilter = virtualNetworkId
    ? securityGroupFilterForVirtualNetworkList(virtualNetworkId)
    : undefined;
  const {
    data: securityGroupsData,
    isLoading: securityGroupsLoading,
    error: securityGroupsError,
    refetch: refetchSecurityGroups,
  } = useListResource(SecurityGroups, securityGroupFilter ? { filter: securityGroupFilter } : {}, {
    enabled: Boolean(virtualNetworkId),
  });

  const securityGroups = useMemo(() => securityGroupsData?.items ?? [], [securityGroupsData]);

  const securityGroupOptions = useMemo(
    () =>
      securityGroups.map((group) => ({
        value: group.id,
        name: group.metadata?.name ?? group.id,
        label:
          showDescription && group.metadata?.description
            ? `${group.metadata?.name ?? group.id} · ${group.metadata.description}`
            : (group.metadata?.name ?? group.id),
      })),
    [securityGroups, showDescription],
  );

  const selectedSgIds = useMemo(() => (sgField.value ?? []).map((sg) => sg.id), [sgField.value]);

  const sgMultiSelectOptions = useMemo<MultiTypeaheadSelectOption[]>(
    () =>
      securityGroupOptions.map((option) => ({
        content: option.label,
        value: option.value,
        selected: selectedSgIds.includes(String(option.value)),
      })),
    [securityGroupOptions, selectedSgIds],
  );

  const securityGroupListLoading = Boolean(virtualNetworkId) && securityGroupsLoading;
  const selectionDisabled = isDisabled || !virtualNetworkId || securityGroupListLoading;
  const singleSelectionLabel = (sgField.value ?? [])
    .map(
      (group) =>
        securityGroupOptions.find((option) => option.value === group.id)?.label ?? group.name,
    )
    .join(', ');

  const onSingleSelect = (_event: MouseEvent<Element> | undefined, value: string | number) => {
    const option = securityGroupOptions.find((group) => group.value === value);
    if (!option) {
      return;
    }
    void sgHelpers.setValue([{ id: option.value, name: option.name }], true);
    void sgHelpers.setTouched(true);
    setIsSingleSelectOpen(false);
  };

  const singleSelectToggle = (toggleRef: Ref<MenuToggleElement>) => (
    <MenuToggle
      ref={toggleRef}
      id={name}
      onClick={() => setIsSingleSelectOpen((wasOpen) => !wasOpen)}
      isExpanded={isSingleSelectOpen}
      isDisabled={selectionDisabled || securityGroupOptions.length === 0}
      isFullWidth
      status={error ? 'danger' : undefined}
      aria-invalid={error ? true : undefined}
      aria-describedby={error ? `${name}-helper-error` : undefined}
      aria-busy={securityGroupListLoading || undefined}
    >
      {singleSelectionLabel ||
        (securityGroupListLoading ? t('Loading...') : t('Select security group'))}
    </MenuToggle>
  );

  return (
    <>
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
      <FormGroup
        label={isSingleSelection ? t('Security group') : t('Security groups')}
        fieldId={name}
        isRequired={isRequired}
      >
        {isSingleSelection ? (
          <Select
            id={`${name}-select`}
            isOpen={isSingleSelectOpen}
            selected={selectedSgIds[0] ?? ''}
            onSelect={onSingleSelect}
            onOpenChange={setIsSingleSelectOpen}
            toggle={singleSelectToggle}
            shouldFocusToggleOnSelect
          >
            <SelectList>
              {securityGroupOptions.map((option) => (
                <SelectOption key={option.value} value={option.value}>
                  {option.label}
                </SelectOption>
              ))}
            </SelectList>
          </Select>
        ) : (
          <MultiTypeaheadSelect
            id={name}
            initialOptions={sgMultiSelectOptions}
            placeholder={securityGroupListLoading ? t('Loading...') : t('Select security groups')}
            isDisabled={selectionDisabled}
            noOptionsFoundMessage={(filter) => t('No options found for "{{filter}}"', { filter })}
            onSelectionChange={(_event, selections) => {
              const newValues: ResourceSelectValue[] = (selections as string[]).map((id) => {
                const option = securityGroupOptions.find((group) => group.value === id);
                return { id, name: option?.name ?? id };
              });
              void sgHelpers.setValue(newValues, true);
              void sgHelpers.setTouched(true);
            }}
            toggleProps={{
              id: name,
              'aria-label': t('Security groups'),
              isDisabled: selectionDisabled,
              isFullWidth: true,
              'aria-busy': securityGroupListLoading || undefined,
              'aria-invalid': error ? true : undefined,
              'aria-describedby': error ? `${name}-helper-error` : undefined,
            }}
          />
        )}
        <FormFieldHelper error={error} fieldId={name} />
      </FormGroup>
    </>
  );
};

export default SecurityGroupsField;
