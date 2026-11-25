import { Button, Content, FormGroup, Stack, StackItem } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import TextField from '@osac/ui-components/components/Form/base/TextField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';

import { useTranslation } from '../../../../hooks/useTranslation';
import { TenantValues } from '../values';

interface TenantGeneralStepProps {
  isEdit?: boolean;
}

const TenantGeneralStep = ({ isEdit = false }: TenantGeneralStepProps) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<TenantValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Content component="p">
          {isEdit ? t('Update tenant details.') : t('Enter tenant details.')}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <TextField
            name="name"
            label={t('Tenant name')}
            fieldId="tenant-name"
            isRequired={!isEdit}
            isDisabled={isEdit}
          />
          <TextField
            name="primaryDomain"
            label={t('Primary email domain')}
            fieldId="tenant-primary-domain"
            helperText={t('Primary domain for tenant sign-in and IdP association.')}
          />
          <FieldArray name="additionalDomains">
            {(helpers) => (
              <FormGroup label={t('Additional email domains')} fieldId="tenant-additional-domains">
                <Stack hasGutter>
                  {values.additionalDomains.map((_domain, index) => (
                    <StackItem key={index}>
                      <TextField
                        name={`additionalDomains.${index}`}
                        label={t('Domain {{number}}', { number: index + 1 })}
                        fieldId={`tenant-domain-${index}`}
                        isRequired
                      >
                        <Button
                          variant="plain"
                          aria-label={t('Remove domain')}
                          onClick={() => helpers.remove(index)}
                          icon={<MinusCircleIcon />}
                        />
                      </TextField>
                    </StackItem>
                  ))}
                  <StackItem>
                    <Button
                      variant="link"
                      icon={<PlusCircleIcon />}
                      onClick={() => helpers.push('')}
                    >
                      {t('Add domain')}
                    </Button>
                  </StackItem>
                </Stack>
              </FormGroup>
            )}
          </FieldArray>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default TenantGeneralStep;
