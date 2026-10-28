import { useMemo } from 'react';
import { Alert, Button, FormGroup } from '@patternfly/react-core';
import { MultiTypeaheadSelect, type MultiTypeaheadSelectOption } from '@patternfly/react-templates';
import { useField } from 'formik';

import { SecurityGroups } from '@osac/types';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useListResource } from '../../../api/use-resource';
import { securityGroupFilterForVirtualNetworkList } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

interface SecurityGroupsFieldProps extends ProvisioningFieldControlProps {
  virtualNetworkName: string;
}

const SecurityGroupsField = ({
  name,
  virtualNetworkName,
  isDisabled = false,
}: SecurityGroupsFieldProps) => {
  const { t } = useTranslation();
  const [virtualNetwork] = useField<ResourceSelectValue>(virtualNetworkName);
  const [sgField, , sgHelpers] = useField<ResourceSelectValue[]>(name);
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
        label: group.metadata?.name ?? group.id,
      })),
    [securityGroups],
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
      <FormGroup label={t('Security groups')} fieldId={name}>
        <MultiTypeaheadSelect
          id={name}
          initialOptions={sgMultiSelectOptions}
          placeholder={securityGroupListLoading ? t('Loading...') : t('Select security groups')}
          isDisabled={isDisabled || !virtualNetworkId || securityGroupListLoading}
          noOptionsFoundMessage={(filter) => t('No options found for "{{filter}}"', { filter })}
          onSelectionChange={(_event, selections) => {
            const newValues: ResourceSelectValue[] = (selections as string[]).map((id) => {
              const option = securityGroupOptions.find((group) => group.value === id);
              return { id, name: option?.label ?? id };
            });
            void sgHelpers.setValue(newValues, true);
            void sgHelpers.setTouched(true);
          }}
          toggleProps={{
            id: name,
            'aria-label': t('Security groups'),
            isDisabled: isDisabled || !virtualNetworkId || securityGroupListLoading,
            isFullWidth: true,
            'aria-busy': securityGroupListLoading || undefined,
          }}
        />
      </FormGroup>
    </>
  );
};

export default SecurityGroupsField;
