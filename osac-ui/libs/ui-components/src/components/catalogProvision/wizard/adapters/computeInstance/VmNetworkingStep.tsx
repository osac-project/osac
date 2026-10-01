import { useEffect, useRef } from 'react';
import { Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import type { ComputeInstanceWizardValues } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { CheckboxField } from '../../../../Form/CheckboxField';
import { NetworkAttachmentPickers } from '../../../../Form/NetworkAttachmentPickers';
import OsacForm from '../../../../Form/OsacForm';
import { useWizardValidation } from '../../WizardValidationContext';

interface Props {
  catalogItem: ComputeInstanceCatalogItem | null;
}

export const VmNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { clearValidationAlert } = useWizardValidation();
  const { values, setFieldTouched, validateForm } = useFormikContext<ComputeInstanceWizardValues>();

  const useDefaultNetwork = values.spec.networking.useDefaultNetwork;

  // Clear validation alerts when toggling back to defaults
  const previousUseDefaultRef = useRef(useDefaultNetwork);
  useEffect(() => {
    const wasCustom = previousUseDefaultRef.current === false;
    previousUseDefaultRef.current = useDefaultNetwork;

    if (!useDefaultNetwork || !wasCustom) {
      return;
    }

    void setFieldTouched('spec.networking.virtualNetwork', false, false);
    void setFieldTouched('spec.networking.subnet', false, false);
    void setFieldTouched('spec.networking.securityGroups', false, false);
    clearValidationAlert();
    void validateForm();
  }, [clearValidationAlert, setFieldTouched, useDefaultNetwork, validateForm]);

  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <CheckboxField
            name="spec.networking.useDefaultNetwork"
            label={t('Use tenant default network')}
            fieldId="vm-use-default-network"
          />
          {!useDefaultNetwork && (
            <NetworkAttachmentPickers fieldPrefix="spec.networking" fieldIdPrefix="vm" />
          )}
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
