import { type ReactNode } from 'react';
import { useField } from 'formik';

import type { ProvisioningFieldValue } from './provisioningFieldValue';

interface ProvisioningFieldRenderProps {
  valueName: string;
  isDisabled: boolean;
}

interface ProvisioningFieldProps {
  name: string;
  children: (props: ProvisioningFieldRenderProps) => ReactNode;
}

const ProvisioningField = ({ name, children }: ProvisioningFieldProps) => {
  const [field] = useField<ProvisioningFieldValue<unknown>>(name);

  return children({
    valueName: `${name}.value`,
    isDisabled: Boolean(field.value?.locked),
  });
};

export default ProvisioningField;
