import { Stack, StackItem } from '@patternfly/react-core';

import type { BareMetalInstanceCatalogItem } from '@osac/types';

import { useTranslation } from '../../../../../hooks/useTranslation';
import { NetworkAttachmentPickers } from '../../../../Form/NetworkAttachmentPickers';
import OsacForm from '../../../../Form/OsacForm';

interface Props {
  catalogItem: BareMetalInstanceCatalogItem | null;
}

export const BareMetalNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();

  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <NetworkAttachmentPickers
            fieldPrefix="spec.networking.attachments.0"
            fieldIdPrefix="bm-attachment-0"
            useDefaultNetworkName="spec.networking.useDefaults"
            autoExternalIpName="spec.networking.attachExternalIp"
            autoExternalIpHelperText={t(
              'The system will auto-create an ExternalIP and ExternalIPAttachment bound to the primary attachment. Auto-created resources are deleted when the instance is deleted.',
            )}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
