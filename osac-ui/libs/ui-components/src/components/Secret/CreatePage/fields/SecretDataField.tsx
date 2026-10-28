import React from 'react';
import { Button, Divider, FormGroup, Stack, StackItem, Title } from '@patternfly/react-core';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import { SecretType } from '@osac/types';

import SecretValueField from './SecretValueField';
import { useTranslation } from '../../../../hooks/useTranslation';
import { type SecretValues, createSecretDataEntry } from '../values';

const SecretDataField = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<SecretValues>();
  const isOpaque = values.type === SecretType.OPAQUE || values.type === SecretType.UNSPECIFIED;

  return (
    <FormGroup fieldId="secret-data" isRequired>
      <FieldArray name="data">
        {(arrayHelpers) => (
          <Stack hasGutter>
            {values.data.map((entry, index) => {
              const isAdditionalTyped = !isOpaque && index > 0;

              return (
                <React.Fragment key={entry.uid}>
                  {index > 0 && (
                    <StackItem>
                      <Divider />
                    </StackItem>
                  )}
                  {isAdditionalTyped && index === 1 && (
                    <StackItem>
                      <Title headingLevel="h3" size="md">
                        {t('Additional keys')}
                      </Title>
                    </StackItem>
                  )}
                  <StackItem>
                    <SecretValueField
                      entry={entry}
                      name={`data.${index}`}
                      keyEditable={isOpaque || isAdditionalTyped}
                      onRemove={
                        (isOpaque && values.data.length > 1) || isAdditionalTyped
                          ? () => arrayHelpers.remove(index)
                          : undefined
                      }
                      canRemove={isOpaque ? values.data.length > 1 : true}
                    />
                  </StackItem>
                </React.Fragment>
              );
            })}
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
