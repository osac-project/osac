import { Stack, StackItem } from '@patternfly/react-core';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { NetworkAttachmentPickers } from '../../../../Form/NetworkAttachmentPickers';
import OsacForm from '../../../../Form/OsacForm';

interface Props {
  catalogItem: ComputeInstanceCatalogItem | null;
}

export const VmNetworkingStep = ({ catalogItem }: Props) => {
  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <NetworkAttachmentPickers fieldPrefix="spec.networking" fieldIdPrefix="vm" />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
