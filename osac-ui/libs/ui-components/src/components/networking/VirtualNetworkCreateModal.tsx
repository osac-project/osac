import { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Alert,
  Button,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import * as Yup from 'yup';

import { useCreateVirtualNetwork } from '../../api/v1/networking';
import { InputField } from '../../components/Form/InputField';
import OsacForm from '../../components/Form/OsacForm';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { buildCidrSchema } from '../../validation/cidr-validation';
import NameField from '../catalogProvision/wizard/fields/NameField';
import ProjectField from '../Form/ProjectField';

type VirtualNetworkCreateFormValues = {
  metadata: {
    name: string;
    project: string;
  };
  ipv4Cidr: string;
};

const VirtualNetworkCreateForm = () => {
  const { t } = useTranslation();

  return (
    <OsacForm>
      <ProjectField />
      <NameField />
      <InputField
        name="ipv4Cidr"
        label={t('IPv4 CIDR')}
        fieldId="vn-ipv4-cidr"
        placeholder="10.0.0.0/16"
      />
    </OsacForm>
  );
};

interface VirtualNetworkCreateModalProps {
  onClose: () => void;
}

export const VirtualNetworkCreateModal = ({ onClose }: VirtualNetworkCreateModalProps) => {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const { mutateAsync: create, error } = useCreateVirtualNetwork();

  const validationSchema = useMemo(
    () =>
      Yup.object({
        metadata: Yup.object({
          name: Yup.string().required(t('Name is required')),
        }),
        ipv4Cidr: buildCidrSchema(t, 'ipv4').required(t('IPv4 CIDR is required')),
      }),
    [t],
  );

  return (
    <Formik<VirtualNetworkCreateFormValues>
      initialValues={{
        metadata: { name: '', project: '' },
        ipv4Cidr: '',
      }}
      validationSchema={validationSchema}
      onSubmit={async (values) => {
        try {
          const result = await create({
            metadata: values.metadata,
            spec: {
              ipv4Cidr: values.ipv4Cidr || undefined,
            },
          });
          navigate(`/networking/virtual-networks/${result.id}`);
        } catch {
          // tanstack handles the error
        }
      }}
    >
      {({ submitForm, isSubmitting }) => (
        <Modal
          variant="small"
          isOpen
          onClose={isSubmitting ? undefined : onClose}
          aria-labelledby="vn-create-modal-title"
        >
          <ModalHeader title={t('Create virtual network')} labelId="vn-create-modal-title" />
          <ModalBody>
            <Stack hasGutter>
              <StackItem>
                <VirtualNetworkCreateForm />
              </StackItem>
              {!!error && (
                <StackItem>
                  <Alert variant="danger" title={t('Failed to create virtual network')} isInline>
                    {getErrorMessage(error)}
                  </Alert>
                </StackItem>
              )}
            </Stack>
          </ModalBody>
          <ModalFooter>
            <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
              {t('Cancel')}
            </Button>
            <Button
              variant="primary"
              onClick={submitForm}
              isDisabled={isSubmitting}
              isLoading={isSubmitting}
            >
              {t('Create')}
            </Button>
          </ModalFooter>
        </Modal>
      )}
    </Formik>
  );
};
