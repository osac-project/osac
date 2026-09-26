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
  isRequired?: boolean;
  isDisabled?: boolean;
  allowEmptySelection?: boolean;
}

const SecretSelectionField = ({
  name,
  label,
  filter,
  isRequired,
  isDisabled,
  allowEmptySelection = false,
}: SecretSelectionFieldProps) => {
  const { data, isLoading, error } = useListResource(Secrets, { filter });
  const { t } = useTranslation();

  return (
    <>
      <SelectField
        name={name}
        label={label}
        fieldId="secret-selection"
        isRequired={isRequired}
        isLoading={isLoading}
        isDisabled={isDisabled || !!error}
        options={[
          ...(allowEmptySelection ? [{ value: '', label: t('None') }] : []),
          ...(data?.items || []).map((d) => ({
            value: d.metadata?.name || '',
            label: d.metadata?.name || '',
          })),
        ]}
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
