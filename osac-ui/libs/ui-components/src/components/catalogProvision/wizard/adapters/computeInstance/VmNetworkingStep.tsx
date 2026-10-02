import { Stack, StackItem } from '@patternfly/react-core';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { useTranslation } from '../../../../../hooks/useTranslation';
import { NetworkAttachmentPickers } from '../../../../Form/NetworkAttachmentPickers';
import OsacForm from '../../../../Form/OsacForm';
import { useWizardValidation } from '../../WizardValidationContext';

interface Props {
  catalogItem: ComputeInstanceCatalogItem | null;
}

export const VmNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { clearValidationAlert } = useWizardValidation();

  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <NetworkAttachmentPickers
            fieldPrefix="spec.networking"
            fieldIdPrefix="vm"
            useDefaultNetworkName="spec.networking.useDefaultNetwork"
            autoExternalIpName="spec.networking.autoExternalIpAttachment"
            autoExternalIpHelperText={t(
              'Automatically provision an external IP for this virtual machine.',
            )}
            onResetToDefaults={clearValidationAlert}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
