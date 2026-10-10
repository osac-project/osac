import { useState } from 'react';
import { Alert, Button, Modal, ModalBody, ModalFooter, ModalHeader } from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { ExternalIPs } from '@osac/types';

import { unallocatedExternalIpFilter } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import OsacForm from '../Form/OsacForm';
import {
  ResourceSelectField,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../Form/ResourceSelectField';

export const EXTERNAL_IP_PICKER_LIMIT = 100;

export interface ExternalIpAttachModalProps {
  title: string;
  emptyDescription: string;
  onAttach: (externalIpId: string) => Promise<unknown>;
  onClose: () => void;
}

interface FormValues {
  externalIp: ResourceSelectValue;
}

const validationSchema = (t: TFunction) =>
  Yup.object({
    externalIp: Yup.object({
      id: Yup.string().required(t('An external IP is required')),
    }),
  });

const ExternalIpAttachModal = ({
  title,
  emptyDescription,
  onAttach,
  onClose,
}: ExternalIpAttachModalProps) => {
  const { t } = useTranslation();
  const [submitError, setSubmitError] = useState<unknown>();

  return (
    <Formik<FormValues>
      initialValues={{ externalIp: emptyResourceSelectValue() }}
      validationSchema={validationSchema(t)}
      onSubmit={async (values) => {
        if (!values.externalIp.id) {
          return;
        }
        setSubmitError(undefined);
        try {
          await onAttach(values.externalIp.id);
          onClose();
        } catch (error) {
          setSubmitError(error);
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
          <ModalHeader title={title} labelId="attach-external-ip-modal-title" />
          <ModalBody>
            <OsacForm>
              <ResourceSelectField
                name="externalIp"
                label={t('External IP')}
                fieldId="attach-external-ip"
                service={ExternalIPs}
                request={{ filter: unallocatedExternalIpFilter(), limit: EXTERNAL_IP_PICKER_LIMIT }}
                isRequired
                autoSelectSingleOption
                placeholder={t('Select an external IP')}
                loadErrorTitle={t('Error loading external IPs')}
                emptyTitle={t('No unattached external IPs available')}
                emptyDescription={emptyDescription}
              />
            </OsacForm>
            {submitError ? (
              <Alert variant="danger" title={t('Failed to attach external IP')} isInline>
                {getErrorMessage(submitError)}
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

export default ExternalIpAttachModal;
