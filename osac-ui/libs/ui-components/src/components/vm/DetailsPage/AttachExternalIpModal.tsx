import { useRef } from 'react';
import { Alert, Button, Modal, ModalBody, ModalFooter, ModalHeader } from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { type ComputeInstance, ExternalIPAttachments, ExternalIPs } from '@osac/types';

import { useApiQueryClient } from '../../../api/use-api-query';
import { useCreateResource, useInvalidateServiceQueries } from '../../../api/use-resource';
import { invalidateComputeInstancesQueries } from '../../../api/v1/compute-instance';
import { unallocatedExternalIpFilter } from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import OsacForm from '../../Form/OsacForm';
import {
  ResourceSelectField,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../Form/ResourceSelectField';

interface AttachExternalIpModalProps {
  vm: ComputeInstance;
  onClose: () => void;
  onSuccess: () => void;
}

interface FormValues {
  externalIp: ResourceSelectValue;
}

const generateExternalIpAttachmentName = (): string => `eipa-${crypto.randomUUID()}`;

const validationSchema = (t: TFunction) =>
  Yup.object({
    externalIp: Yup.object({
      id: Yup.string().required(t('An external IP is required')),
    }),
  });

const AttachExternalIpModal = ({ vm, onClose, onSuccess }: AttachExternalIpModalProps) => {
  const { t } = useTranslation();
  const submittingRef = useRef(false);
  const queryClient = useApiQueryClient();
  const invalidateService = useInvalidateServiceQueries();
  const createAttachment = useCreateResource(ExternalIPAttachments, {
    onSuccess: async () => {
      await invalidateService(ExternalIPs);
      await invalidateComputeInstancesQueries(queryClient);
    },
  });

  return (
    <Formik<FormValues>
      initialValues={{ externalIp: emptyResourceSelectValue() }}
      validationSchema={validationSchema(t)}
      onSubmit={async (values) => {
        if (submittingRef.current) {
          return;
        }
        submittingRef.current = true;
        try {
          await createAttachment.mutateAsync({
            object: {
              metadata: { name: generateExternalIpAttachmentName() },
              spec: {
                externalIp: { id: values.externalIp.id },
                target: { case: 'computeInstance', value: { id: vm.id } },
              },
            },
          });
          onSuccess();
        } catch {
          // surfaced via createAttachment.error below
        } finally {
          submittingRef.current = false;
        }
      }}
    >
      {({ submitForm, isSubmitting, values }) => (
        <Modal
          variant="small"
          isOpen
          onClose={isSubmitting ? undefined : onClose}
          aria-labelledby="attach-external-ip-modal-title"
        >
          <ModalHeader title={t('Attach external IP')} labelId="attach-external-ip-modal-title" />
          <ModalBody>
            <OsacForm>
              <ResourceSelectField
                name="externalIp"
                label={t('External IP')}
                fieldId="attach-external-ip"
                service={ExternalIPs}
                request={{ filter: unallocatedExternalIpFilter() }}
                isRequired
                autoSelectSingleOption
                placeholder={t('Select an external IP')}
                loadErrorTitle={t('Error loading external IPs')}
                emptyTitle={t('No unattached external IPs available')}
                emptyDescription={t(
                  'Create an external IP first, then attach it to this virtual machine.',
                )}
              />
            </OsacForm>
            {createAttachment.error ? (
              <Alert variant="danger" title={t('Failed to attach external IP')} isInline>
                {getErrorMessage(createAttachment.error)}
              </Alert>
            ) : null}
          </ModalBody>
          <ModalFooter>
            <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
              {t('Cancel')}
            </Button>
            <Button
              variant="primary"
              onClick={submitForm}
              isDisabled={isSubmitting || !values.externalIp.id}
              isLoading={isSubmitting}
            >
              {t('Attach')}
            </Button>
          </ModalFooter>
        </Modal>
      )}
    </Formik>
  );
};

export default AttachExternalIpModal;
