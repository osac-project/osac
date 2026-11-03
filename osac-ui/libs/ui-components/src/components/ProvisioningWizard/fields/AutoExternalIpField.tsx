import { FormSection, Switch } from '@patternfly/react-core';
import { useField } from 'formik';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useTranslation } from '../../../hooks/useTranslation';
import { FormFieldHelper } from '../../Form/FormFieldHelper';

interface AutoExternalIpFieldProps extends ProvisioningFieldControlProps {
  helperText?: string;
}

const AutoExternalIpField = ({
  name,
  helperText,
  isDisabled = false,
}: AutoExternalIpFieldProps) => {
  const { t } = useTranslation();
  const [field, , helpers] = useField<boolean>(name);

  return (
    <FormSection title={t('External IP')} titleElement="h2">
      <Switch
        id={name}
        label={t('Auto-attach external IP')}
        isChecked={field.value}
        isDisabled={isDisabled}
        aria-describedby={`${name}-helper-description`}
        onChange={(_event, checked) => void helpers.setValue(checked)}
      />
      <FormFieldHelper
        fieldId={name}
        description={
          field.value
            ? (helperText ??
              t(
                'Attaches an external IP so the instance is reachable outside the private network.',
              ))
            : t('No external IP at launch. You can attach one later from the instance.')
        }
      />
    </FormSection>
  );
};

export default AutoExternalIpField;
