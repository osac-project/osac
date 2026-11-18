import { Content, FormSection, Stack, StackItem, Title } from '@patternfly/react-core';

import TextField from '@osac/ui-components/components/Form/base/TextField';
import NameField from '@osac/ui-components/components/Form/NameField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const PoolStep = () => {
  const { t } = useTranslation();

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
            'Define routable addresses for tenant edge exposure. The pool is shared by all tenants.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <NameField isDisabled={false} />
          <FormSection title={t('IPv4 CIDR')}>
            <TextField
              name="cidrs.0"
              label={t('CIDR')}
              fieldId="external-ip-pool-cidr-0"
              placeholder="10.128.0.0/14"
              isRequired
            />
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default PoolStep;
