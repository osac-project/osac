import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useTranslation } from '../../../hooks/useTranslation';
import { CheckboxField } from '../../Form/CheckboxField';

type UseDefaultNetworkFieldProps = ProvisioningFieldControlProps;

const UseDefaultNetworkField = ({ name, isDisabled = false }: UseDefaultNetworkFieldProps) => {
  const { t } = useTranslation();

  return (
    <CheckboxField
      name={name}
      label={t('Use tenant default network')}
      fieldId={name}
      isDisabled={isDisabled}
    />
  );
};

export default UseDefaultNetworkField;
