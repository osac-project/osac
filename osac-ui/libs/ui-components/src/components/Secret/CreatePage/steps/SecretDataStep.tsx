import { Stack, StackItem, Title } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { SecretType } from '@osac/types';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';

import { useTranslation } from '../../../../hooks/useTranslation';
import SecretDataField from '../fields/SecretDataField';
import SecretTypeField from '../fields/SecretTypeField';
import SecretValueField from '../fields/SecretValueField';
import type { SecretValues } from '../values';

interface SecretDataStepProps {
  isEdit: boolean;
}

const SecretDataStep = ({ isEdit }: SecretDataStepProps) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<SecretValues>();

  const secretDataField = (() => {
    switch (values.type) {
      case SecretType.KUBECONFIG:
        return (
          <SecretValueField label={t('Kubeconfig')} entry={values.kubeconfig} name="kubeconfig" />
        );
      case SecretType.PULL_SECRET:
        return (
          <SecretValueField label={t('Pull secret')} entry={values.pullsecret} name="pullsecret" />
        );
      case SecretType.USER_DATA:
        return <SecretValueField label={t('User data')} entry={values.userData} name="userData" />;
      case SecretType.VALUE:
        return <SecretValueField label={t('Value')} entry={values.value} name="value" />;
      case SecretType.OPAQUE:
      case SecretType.UNSPECIFIED:
        return <SecretDataField />;
    }
  })();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Secret data')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          <SecretTypeField isEdit={isEdit} />
          {secretDataField}
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default SecretDataStep;
