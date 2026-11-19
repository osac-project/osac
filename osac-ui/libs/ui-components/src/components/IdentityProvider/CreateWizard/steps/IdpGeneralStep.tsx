import { Stack, StackItem, Title } from '@patternfly/react-core';

import TextField from '@osac/ui-components/components/Form/base/TextField';
import DescriptionField from '@osac/ui-components/components/Form/DescriptionField';
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
          <TextField
            name="spec.title"
            label={t('Title')}
            helperText={t('Human-friendly short title for this IdP')}
            fieldId="idp-title"
          />
          <DescriptionField name="spec.description" fieldId="idp-description" />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default IdpGeneralStep;
