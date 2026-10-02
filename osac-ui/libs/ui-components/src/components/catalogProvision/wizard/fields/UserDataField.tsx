import { Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { InputField } from '@osac/ui-components/components/Form/InputField';
import { RadioButtonField } from '@osac/ui-components/components/Form/RadioButtonField';
import SecretSelectionField from '@osac/ui-components/components/Form/SecretSelectionField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

type UserDataFieldProps = {
  sourceName: string;
  secretRefName: string;
  inlineName: string;
  currentSource: 'secret' | 'inline';
  projectName: string;
};

const UserDataField = ({
  sourceName,
  secretRefName,
  inlineName,
  currentSource,
  projectName,
}: UserDataFieldProps) => {
  const { t } = useTranslation();

  return (
    <>
      <RadioButtonField
        name={sourceName}
        label={t('User data')}
        fieldId="user-data-source"
        options={[
          { value: 'inline', label: t('Inline text') },
          { value: 'secret', label: t('Secret reference') },
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
          asFormGroup={false}
        />
      ) : (
        <InputField
          label={t('User data')}
          name={inlineName}
          fieldId="inline-user-data"
          multiline
          helperText={t('Optional cloud-init user data (max 64 KB).')}
          asFormGroup={false}
        />
      )}
    </>
  );
};

export default UserDataField;
