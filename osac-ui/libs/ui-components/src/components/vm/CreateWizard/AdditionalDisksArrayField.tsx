import { Button, Flex, FlexItem, Stack, StackItem, Title } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { useField } from 'formik';

import type { ComputeInstanceDiskValues } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { InputField } from '../../Form/InputField';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import { StorageTierSelectField } from '../../Form/StorageTierSelectField';
import type { ProvisioningFieldControlProps } from '../../ProvisioningWizard/fields/provisioningFieldValue';

interface AdditionalDisksArrayFieldProps extends ProvisioningFieldControlProps {
  emptyMessage?: string;
}

/** Renders the inline disk-set editor from the VMaaS storage UX. */
export const AdditionalDisksArrayField = ({
  name = '',
  isDisabled = false,
  emptyMessage,
}: AdditionalDisksArrayFieldProps) => {
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
      {disks.length === 0 && emptyMessage ? <StackItem>{emptyMessage}</StackItem> : null}
      {disks.map((_, index) => (
        <StackItem key={index}>
          <Stack hasGutter>
            <StackItem>
              <Flex
                alignItems={{ default: 'alignItemsCenter' }}
                justifyContent={{ default: 'justifyContentSpaceBetween' }}
              >
                <FlexItem>
                  <Title headingLevel="h3" size="md" id={`vm-additional-disk-group-${index}`}>
                    {t('Disk set {{number}}', { number: index + 1 })}
                  </Title>
                </FlexItem>
                {!isDisabled ? (
                  <FlexItem>
                    <Button
                      variant="link"
                      isInline
                      isDanger
                      aria-label={t('Remove disk')}
                      onClick={() => removeDisk(index)}
                      icon={<MinusCircleIcon />}
                    >
                      {t('Remove')}
                    </Button>
                  </FlexItem>
                ) : null}
              </Flex>
            </StackItem>
            <StackItem>
              <InputField
                name={`${name}.${index}.sizeGib`}
                label={t('Disk size (GiB)')}
                fieldId={`vm-additional-disk-${index}-size`}
                type="number"
                isRequired
                min={1}
                max={16384}
                step={1}
                isDisabled={isDisabled}
              />
            </StackItem>
            <StackItem>
              <StorageTierSelectField
                name={`${name}.${index}.storageTier`}
                label={t('Storage tier')}
                fieldId={`vm-additional-disk-${index}-storage-tier`}
                isRequired
                isDisabled={isDisabled}
                autoSelectFirstOption
                description={t('Storage tiers are defined by your cloud provider administrator.')}
              />
            </StackItem>
          </Stack>
        </StackItem>
      ))}
      <StackItem>
        <Button
          variant="link"
          isInline
          icon={<PlusCircleIcon />}
          onClick={addDisk}
          isDisabled={isDisabled}
        >
          {t('Add disk')}
        </Button>
      </StackItem>
    </Stack>
  );
};
