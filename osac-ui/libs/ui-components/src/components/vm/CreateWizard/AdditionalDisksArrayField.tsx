import { Button, Stack, StackItem } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { useField } from 'formik';

import type { ComputeInstanceDiskValues } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { InputField } from '../../Form/InputField';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import { StorageTierSelectField } from '../../Form/StorageTierSelectField';
import ProvisioningFieldGroup from '../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from '../../ProvisioningWizard/fields/provisioningFieldValue';

/** Same mechanism as ClusterNodeSetsArrayField: every row is always live and editable; Add appends a row, Remove deletes one. No separate editor/confirm/cancel state. */
export const AdditionalDisksArrayField = ({
  name = '',
  isDisabled = false,
}: ProvisioningFieldControlProps) => {
  const { t } = useTranslation();
  const [field, , helpers] = useField<ComputeInstanceDiskValues[]>(name);
  const disks = field.value ?? [];

  const addDisk = () => {
    void helpers.setValue([...disks, { sizeGib: '30', storageTier: emptyResourceSelectValue() }]);
  };

  const removeDisk = (index: number) => {
    void helpers.setValue(disks.filter((_, i) => i !== index));
  };

  return (
    <Stack hasGutter>
      {disks.length === 0 ? <StackItem>{t('No additional disks added.')}</StackItem> : null}
      {disks.map((_, index) => (
        <StackItem key={index}>
          <ProvisioningFieldGroup
            title={t('Disk {{number}}', { number: index + 1 })}
            titleId={`vm-additional-disk-group-${index}`}
            actions={
              isDisabled ? undefined : (
                <Button
                  variant="plain"
                  aria-label={t('Remove disk')}
                  onClick={() => removeDisk(index)}
                  icon={<MinusCircleIcon />}
                />
              )
            }
          >
            <InputField
              name={`${name}.${index}.sizeGib`}
              label={t('Size (GiB)')}
              fieldId={`vm-additional-disk-${index}-size`}
              type="number"
              isRequired
              min={1}
              max={16384}
              step={1}
              helperText={t('Size in GiB')}
              isDisabled={isDisabled}
            />
            <StorageTierSelectField
              name={`${name}.${index}.storageTier`}
              label={t('Storage tier')}
              fieldId={`vm-additional-disk-${index}-storage-tier`}
              isRequired
              isDisabled={isDisabled}
            />
          </ProvisioningFieldGroup>
        </StackItem>
      ))}
      <StackItem>
        <Button variant="link" icon={<PlusCircleIcon />} onClick={addDisk} isDisabled={isDisabled}>
          {t('Add disk')}
        </Button>
      </StackItem>
    </Stack>
  );
};
