import { Stack, StackItem, Title } from '@patternfly/react-core';

import { InputField } from '@osac/ui-components/components/Form/InputField';
import NameField from '@osac/ui-components/components/Form/NameField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';

import { useTranslation } from '../../../../hooks/useTranslation';

interface IdpGeneralStepProps {
  isEdit: boolean;
}

const IdpGeneralStep = ({ isEdit }: IdpGeneralStepProps) => {
  const { t } = useTranslation();
  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('General')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          <NameField isDisabled={isEdit} />
          <InputField
            name="spec.title"
            label={t('Title')}
            helperText={t('Human-friendly short title for this IdP')}
            fieldId="idp-title"
          />
          <InputField
            name="spec.description"
            label={t('Description')}
            fieldId="idp-description"
            multiline
            rows={3}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default IdpGeneralStep;
