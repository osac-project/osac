import type { ReactNode } from 'react';
import { Switch } from '@patternfly/react-core';
import { useField } from 'formik';

import { useTranslation } from '../../../hooks/useTranslation';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

interface CatalogFieldRenderProps {
  valueName: string;
  isDisabled: boolean;
  labelInfo: ReactNode;
}

interface CatalogFieldProps {
  name: string;
  children: (props: CatalogFieldRenderProps) => ReactNode;
}

const CatalogField = ({ name, children }: CatalogFieldProps) => {
  const { t } = useTranslation();
  const [field, , helpers] = useField<ProvisioningFieldValue<unknown>>(name);
  const isEditable = !field.value?.locked;

  const labelInfo = (
    <Switch
      id={`${name}-editable`}
      label={isEditable ? t('Editable') : t('Locked')}
      isChecked={isEditable}
      onChange={(_event, checked) => {
        void helpers.setValue({ ...field.value, locked: !checked });
      }}
    />
  );

  return children({
    valueName: `${name}.value`,
    // The author must still be able to enter a value for a locked field, so the
    // wrapped control stays enabled regardless of the Editable/Locked switch.
    isDisabled: false,
    labelInfo,
  });
};

export default CatalogField;
