import { useField } from 'formik';

import { Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';

import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from './constants';
import { InputField } from './InputField';
import { RadioButtonField } from './RadioButtonField';
import SecretSelectionField from './SecretSelectionField';
import { useTranslation } from '../../hooks/useTranslation';

interface UserDataFieldProps {
  sourceName: string;
  secretRefName: string;
  inlineName: string;
  projectName: string;
  isDisabled?: boolean;
}

const UserDataField = ({
  sourceName,
  secretRefName,
  inlineName,
  projectName,
  isDisabled = false,
}: UserDataFieldProps) => {
  const { t } = useTranslation();
  const [sourceField] = useField<string>(sourceName);
  const currentSource = sourceField.value === 'secret' ? 'secret' : 'inline';

  return (
    <>
      <RadioButtonField
        name={sourceName}
        label={t('User data')}
        fieldId="user-data-source"
        isDisabled={isDisabled}
        options={[
          { value: 'inline', label: t('Enter value') },
          { value: 'secret', label: t('Select secret') },
        ]}
      />
      {currentSource === 'secret' ? (
        <SecretSelectionField
          label={t('User data secret')}
          filter={cel<Secret>((filter) =>
            filter.and(
              filter.field('metadata.project').equals(projectName),
              filter.field('type').equals(SecretType.USER_DATA),
            ),
          )}
          name={secretRefName}
          placeholder={t('Select a user data secret')}
          isDisabled={isDisabled}
          asFormGroup={false}
        />
      ) : (
        <InputField
          label={t('Cloud-init')}
          name={inlineName}
          fieldId="inline-user-data"
          multiline
          rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
          resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
          helperText={t('Optional cloud-init user data (max 64 KB).')}
          isDisabled={isDisabled}
          asFormGroup={false}
        />
      )}
    </>
  );
};

export default UserDataField;
