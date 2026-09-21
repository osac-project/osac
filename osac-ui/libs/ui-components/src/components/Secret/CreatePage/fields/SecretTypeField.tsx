import { SecretType } from '@osac/types';
import {
  SelectField,
  type SelectFieldOption,
} from '@osac/ui-components/components/Form/SelectField';

import { useTranslation } from '../../../../hooks/useTranslation';
import { getSecretType } from '../../utils';

interface SecretTypeFieldProps {
  isEdit: boolean;
}

const SECRET_TYPES = [
  SecretType.OPAQUE,
  SecretType.KUBECONFIG,
  SecretType.PULL_SECRET,
  SecretType.USER_DATA,
  SecretType.VALUE,
] as const;

const SecretTypeField = ({ isEdit }: SecretTypeFieldProps) => {
  const { t } = useTranslation();
  const secretTypes = getSecretType(t);
  const options: SelectFieldOption[] = SECRET_TYPES.map((type) => ({
    value: type,
    label: secretTypes[type],
  }));

  return (
    <SelectField
      name="type"
      label={t('Type')}
      fieldId="secret-type"
      options={options}
      isRequired
      isDisabled={isEdit}
    />
  );
};

export default SecretTypeField;
