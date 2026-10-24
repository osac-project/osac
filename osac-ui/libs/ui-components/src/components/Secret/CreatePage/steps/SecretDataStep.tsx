import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import OsacForm from '@osac/ui-components/components/Form/OsacForm';

import { useTranslation } from '../../../../hooks/useTranslation';
import SecretDataField from '../fields/SecretDataField';
import SecretTypeField from '../fields/SecretTypeField';

interface SecretDataStepProps {
  isEdit: boolean;
}

const SecretDataStep = ({ isEdit }: SecretDataStepProps) => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Secret data')}
        </Title>
        <Content component="p">
          {t('Select a secret type, then enter or upload its key(s) and values.')}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <SecretTypeField isEdit={isEdit} />
          <SecretDataField />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default SecretDataStep;
