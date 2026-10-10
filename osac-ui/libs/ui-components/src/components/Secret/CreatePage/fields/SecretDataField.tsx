import React from 'react';
import { Button, Divider, FormGroup, Stack, StackItem } from '@patternfly/react-core';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import SecretValueField from './SecretValueField';
import { useTranslation } from '../../../../hooks/useTranslation';
import { type SecretValues, createSecretDataEntry } from '../values';

const SecretDataField = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<SecretValues>();

  return (
    <FormGroup fieldId="secret-data" isRequired>
      <FieldArray name="opaque">
        {(arrayHelpers) => (
          <Stack hasGutter>
            {values.opaque.map((entry, index) => (
              <React.Fragment key={entry.uid}>
                {index > 0 && (
                  <StackItem>
                    <Divider />
                  </StackItem>
                )}
                <StackItem>
                  <SecretValueField
                    label={t('Value')}
                    entry={entry}
                    name={`opaque.${index}`}
                    showKey
                    onRemove={() => arrayHelpers.remove(index)}
                    canRemove={values.opaque.length > 1}
                  />
                </StackItem>
              </React.Fragment>
            ))}
            <StackItem>
              <Button
                variant="link"
                icon={<PlusCircleIcon />}
                onClick={() => arrayHelpers.push(createSecretDataEntry('', new Uint8Array()))}
              >
                {t('Add key')}
              </Button>
            </StackItem>
          </Stack>
        )}
      </FieldArray>
    </FormGroup>
  );
};

export default SecretDataField;
