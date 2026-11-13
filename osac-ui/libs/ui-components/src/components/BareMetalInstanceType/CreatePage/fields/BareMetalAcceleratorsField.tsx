import {
  Button,
  FormFieldGroup,
  FormFieldGroupHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import { useTranslation } from '../../../../hooks/useTranslation';
import IntegerField from '../../../Form/base/IntegerField';
import TextField from '../../../Form/base/TextField';
import { type BareMetalInstanceTypeFormValues, emptyAcceleratorValue } from '../values';

const BareMetalAcceleratorsField = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceTypeFormValues>();
  const accelerators = values.spec.accelerators;

  return (
    <FieldArray name="spec.accelerators">
      {(helpers) => (
        <Stack hasGutter>
          {accelerators.length === 0 && <StackItem>{t('No accelerators added.')}</StackItem>}
          {accelerators.map((_, index) => (
            <StackItem key={index}>
              <FormFieldGroup
                header={
                  <FormFieldGroupHeader
                    titleText={{
                      text: t('Accelerator {{number}}', { number: index + 1 }),
                      id: `baremetal-accelerator-group-${index}`,
                    }}
                    actions={
                      <Button
                        variant="plain"
                        aria-label={t('Remove accelerator')}
                        onClick={() => helpers.remove(index)}
                        icon={<MinusCircleIcon />}
                      />
                    }
                  />
                }
              >
                <TextField
                  name={`spec.accelerators.${index}.type`}
                  label={t('Type')}
                  fieldId={`baremetal-accelerator-${index}-type`}
                  isRequired
                  placeholder={t('e.g. GPU, FPGA, TPU')}
                />
                <TextField
                  name={`spec.accelerators.${index}.model`}
                  label={t('Model')}
                  fieldId={`baremetal-accelerator-${index}-model`}
                  isRequired
                  placeholder={t('e.g. A100, H100')}
                />
                <TextField
                  name={`spec.accelerators.${index}.vendor`}
                  label={t('Vendor')}
                  fieldId={`baremetal-accelerator-${index}-vendor`}
                  placeholder={t('e.g. NVIDIA, AMD, Intel')}
                />
                <IntegerField
                  name={`spec.accelerators.${index}.memoryGb`}
                  label={t('Memory (GiB)')}
                  fieldId={`baremetal-accelerator-${index}-memory`}
                />
              </FormFieldGroup>
            </StackItem>
          ))}
          <StackItem>
            <Button
              variant="link"
              icon={<PlusCircleIcon />}
              onClick={() => helpers.push(emptyAcceleratorValue())}
            >
              {t('Add accelerator')}
            </Button>
          </StackItem>
        </Stack>
      )}
    </FieldArray>
  );
};

export default BareMetalAcceleratorsField;
