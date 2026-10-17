import type { ReactNode } from 'react';
import { FormFieldGroup, FormFieldGroupHeader } from '@patternfly/react-core';

interface ProvisioningFieldGroupProps {
  title: string;
  titleId: string;
  actions?: ReactNode;
  children: ReactNode;
}

const ProvisioningFieldGroup = ({
  title,
  titleId,
  actions,
  children,
}: ProvisioningFieldGroupProps) => (
  <FormFieldGroup
    header={<FormFieldGroupHeader titleText={{ text: title, id: titleId }} actions={actions} />}
  >
    {children}
  </FormFieldGroup>
);

export default ProvisioningFieldGroup;
