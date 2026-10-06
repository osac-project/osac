import {
  Button,
  FormFieldGroup,
  FormFieldGroupHeader,
  FormGroup,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import SecretValueField from './SecretValueField';
import { useTranslation } from '../../../../hooks/useTranslation';
import type { SecretDataEntry, SecretValues } from '../values';

const SecretDataField = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<SecretValues>();

  return (
    <FormGroup label={t('Secret data')} fieldId="secret-data" isRequired>
      <FieldArray name="opaque">
        {(arrayHelpers) => (
          <Stack hasGutter>
            {values.opaque.map((entry, index) => (
              <StackItem key={index}>
                <FormFieldGroup
                  header={
                    <FormFieldGroupHeader
                      titleText={{
                        text: t('Secret entry {{number}}', { number: index + 1 }),
                        id: `secret-entry-group-${index}`,
                      }}
                      actions={
                        <Button
                          variant="plain"
                          aria-label={t('Remove secret entry')}
                          onClick={() => arrayHelpers.remove(index)}
                          isDisabled={values.opaque.length === 1}
                          icon={<MinusCircleIcon />}
                        />
                      }
                    />
                  }
                >
                  <SecretValueField
                    label={t('Value')}
                    entry={entry}
                    name={`opaque.${index}`}
                    showKey
                  />
                </FormFieldGroup>
              </StackItem>
            ))}
            <StackItem>
              <Button
                variant="link"
                icon={<PlusCircleIcon />}
                onClick={() =>
                  arrayHelpers.push<SecretDataEntry>({ key: '', value: new Uint8Array() })
                }
              >
                {t('Add secret entry')}
              </Button>
            </StackItem>
          </Stack>
        )}
      </FieldArray>
    </FormGroup>
  );
};

export default SecretDataField;
