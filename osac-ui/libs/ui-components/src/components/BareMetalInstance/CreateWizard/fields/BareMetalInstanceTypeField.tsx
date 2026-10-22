import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { BareMetalInstanceTypes } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import BareMetalInstanceTypeDescription from './BareMetalInstanceTypeDescription';
import { useTranslation } from '../../../../hooks/useTranslation';
import { SelectField } from '../../../Form/SelectField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface BareMetalInstanceTypeFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const BareMetalInstanceTypeField = ({
  name,
  fieldId = 'instance-type',
  isDisabled = false,
}: BareMetalInstanceTypeFieldProps) => {
  const { t } = useTranslation();
  const { getFieldProps } = useFormikContext();
  const selectedName = getFieldProps<string>(name).value;

  const { data, isLoading, error, refetch } = useListResource(BareMetalInstanceTypes);

  const currentInstanceType = data?.items.find((i) => i.metadata?.name === selectedName);

  return (
    <Stack hasGutter>
      {!!error && (
        <StackItem>
          <Alert variant="danger" isInline title={t('Could not load instance types')}>
            <Stack hasGutter>
              <StackItem>{getErrorMessage(error)}</StackItem>
              <StackItem>
                <Button variant="link" isInline onClick={() => void refetch()}>
                  {t('Retry')}
                </Button>
              </StackItem>
            </Stack>
          </Alert>
        </StackItem>
      )}
      <StackItem>
        <SelectField
          name={name}
          label={t('Instance type')}
          fieldId={fieldId}
          isLoading={isLoading}
          isDisabled={isDisabled || !!error}
          autoSelectSingleOption
          placeholder={t('Select an instance type')}
          options={(data?.items || []).map((instanceType) => ({
            label: `${instanceType.metadata?.name || ''}`,
            value: instanceType.metadata?.name || '',
            description: instanceType.spec?.description,
          }))}
        />
      </StackItem>
      {currentInstanceType && (
        <StackItem>
          <BareMetalInstanceTypeDescription instanceType={currentInstanceType} />
        </StackItem>
      )}
    </Stack>
  );
};

export default BareMetalInstanceTypeField;
