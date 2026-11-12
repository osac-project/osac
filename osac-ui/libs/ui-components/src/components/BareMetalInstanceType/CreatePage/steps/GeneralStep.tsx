import { Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import TextAreaField from '../../../Form/base/TextAreaField';
import { KeyValueMapField } from '../../../Form/KeyValueMapField';
import NameField from '../../../Form/NameField';
import OsacForm from '../../../Form/OsacForm';

interface GeneralStepProps {
  isEdit: boolean;
}

const GeneralStep = ({ isEdit }: GeneralStepProps) => {
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
          <TextAreaField
            name="spec.description"
            label={t('Description')}
            fieldId="baremetal-instance-type-description"
          />
          <KeyValueMapField
            name="spec.hostLabelSelector"
            fieldId="baremetal-instance-type-host-label-selector"
            label={t('Host labels')}
            isRequired
            addLabel={t('Add host label')}
            removeLabel={t('Remove host label')}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default GeneralStep;
