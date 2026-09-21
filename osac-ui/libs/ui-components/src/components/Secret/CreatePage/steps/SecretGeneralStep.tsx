import { Stack, StackItem, Title } from '@patternfly/react-core';

import NameField from '@osac/ui-components/components/catalogProvision/wizard/fields/NameField';
import { InputField } from '@osac/ui-components/components/Form/InputField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import { useTranslation } from '../../../../hooks/useTranslation';

interface SecretGeneralStepProps {
  isEdit: boolean;
}

const SecretGeneralStep = ({ isEdit }: SecretGeneralStepProps) => {
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
          <ProjectField isDisabled={isEdit} />
          <NameField isDisabled={isEdit} />
          <InputField
            name="metadata.description"
            label={t('Description')}
            fieldId="description-field"
            multiline
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default SecretGeneralStep;
