import { FormFieldGroup, FormFieldGroupHeader, Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { BareMetalInstanceWizardValues } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { NetworkAttachmentPickers } from '../../../../Form/NetworkAttachmentPickers';
import OsacForm from '../../../../Form/OsacForm';

export const BareMetalNetworkAttachmentsField = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();
  const attachments = values.spec.networking.attachments;

  return (
    <Stack hasGutter>
      <StackItem>
        <Stack>
          {attachments.slice(0, 1).map((attachment) => (
            <StackItem key={attachment.id}>
              <FormFieldGroup
                header={
                  <FormFieldGroupHeader
                    titleText={{
                      text: t('Network attachment'),
                      id: 'bm-attachment-group-0',
                    }}
                  />
                }
              >
                <OsacForm>
                  <NetworkAttachmentPickers
                    fieldPrefix="spec.networking.attachments.0"
                    fieldIdPrefix="bm-attachment-0"
                  />
                </OsacForm>
              </FormFieldGroup>
            </StackItem>
          ))}
        </Stack>
      </StackItem>
    </Stack>
  );
};
