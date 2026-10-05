import { Button, Content, FormSection, Stack, StackItem, Title } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import NameField from '@osac/ui-components/components/catalogProvision/wizard/fields/NameField';
import { InputField } from '@osac/ui-components/components/Form/InputField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import type { ExternalIpPoolFormValues } from './values';

const PoolStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ExternalIpPoolFormValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('External IP pool')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Define routable addresses for tenant edge exposure. You will assign this pool to a tenant in the next step.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <NameField isDisabled={false} />
          <FormSection title={t('IPv4 CIDRs')}>
            <FieldArray name="cidrs">
              {(helpers) => (
                <Stack hasGutter>
                  {values.cidrs.map((_cidr, index) => (
                    <StackItem key={index}>
                      <InputField
                        name={`cidrs.${index}`}
                        label={t('CIDR {{number}}', { number: index + 1 })}
                        fieldId={`external-ip-pool-cidr-${index}`}
                        placeholder="10.128.0.0/14"
                        isRequired
                      >
                        {values.cidrs.length > 1 && (
                          <Button
                            variant="plain"
                            aria-label={t('Remove CIDR {{number}}', {
                              number: index + 1,
                            })}
                            onClick={() => helpers.remove(index)}
                            icon={<MinusCircleIcon />}
                          />
                        )}
                      </InputField>
                    </StackItem>
                  ))}
                  <StackItem>
                    <Button
                      variant="link"
                      icon={<PlusCircleIcon />}
                      onClick={() => helpers.push('')}
                    >
                      {t('Add CIDR')}
                    </Button>
                  </StackItem>
                </Stack>
              )}
            </FieldArray>
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default PoolStep;
