import { Alert } from '@patternfly/react-core';

import { Secrets } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { SelectField } from './SelectField';
import { useTranslation } from '../../hooks/useTranslation';

interface SecretSelectionFieldProps {
  name: string;
  label: string;
  filter: string | undefined;
  fieldId?: string;
  isRequired?: boolean;
  isDisabled?: boolean;
  placeholder?: string;
  asFormGroup?: boolean;
}

const SecretSelectionField = ({
  name,
  label,
  filter,
  fieldId = name.replace(/\./g, '-'),
  isRequired,
  isDisabled,
  placeholder,
  asFormGroup = true,
}: SecretSelectionFieldProps) => {
  const { data, isLoading, error } = useListResource(Secrets, { filter });
  const { t } = useTranslation();

  return (
    <>
      <SelectField
        name={name}
        label={label}
        fieldId={fieldId}
        isRequired={isRequired}
        isLoading={isLoading}
        isDisabled={isDisabled || !!error}
        placeholder={placeholder}
        options={
          data?.items.length
            ? [
                ...(data?.items || []).map((d) => ({
                  value: d.metadata?.name || '',
                  label: d.metadata?.name || '',
                })),
              ]
            : [
                {
                  value: '',
                  label: t('No secret available'),
                  isDisabled: true,
                },
              ]
        }
        asFormGroup={asFormGroup}
      />
      {error && (
        <Alert variant="danger" isInline title={t('Failed to fetch secrets')}>
          {getErrorMessage(error)}
        </Alert>
      )}
    </>
  );
};

export default SecretSelectionField;
