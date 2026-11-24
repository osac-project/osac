import { Content, Stack, StackItem } from '@patternfly/react-core';

import TextField from '@osac/ui-components/components/Form/base/TextField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';

import { useTranslation } from '../../../../hooks/useTranslation';

const TenantBillingStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Content component="p">
          {t(
            'Enter an optional external billing ID for this tenant. You can leave it blank and add it later.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <TextField
            name="externalBillingId"
            label={t('External billing ID')}
            fieldId="tenant-external-billing-id"
            placeholder={t('e.g. A00078421')}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default TenantBillingStep;
