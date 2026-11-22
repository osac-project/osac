import { useMemo, useState } from 'react';
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

import { type VirtualNetwork, VirtualNetworks } from '@osac/types';

import { useCreateResource } from '../../api/use-resource';
import TextField from '../../components/Form/base/TextField';
import OsacForm from '../../components/Form/OsacForm';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { buildCidrSchema } from '../../validation/cidr-validation';
import NameField from '../Form/NameField';
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
      <TextField
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
  onCreated?: (network: VirtualNetwork) => void;
  initialProject?: string;
}

export const VirtualNetworkCreateModal = ({
  onClose,
  onCreated,
  initialProject = '',
}: VirtualNetworkCreateModalProps) => {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const { mutateAsync: create, error } = useCreateResource(VirtualNetworks);
  const [submitError, setSubmitError] = useState<unknown>(null);

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
        metadata: { name: '', project: initialProject },
        ipv4Cidr: '',
      }}
      validationSchema={validationSchema}
      onSubmit={async (values) => {
        setSubmitError(null);
        let result: VirtualNetwork;
        try {
          const response = await create({
            object: {
              metadata: values.metadata,
              spec: {
                ipv4Cidr: values.ipv4Cidr || undefined,
              },
            },
          });
          if (!response.object?.id) {
            throw new Error('Create response missing id');
          }
          result = response.object;
        } catch (submissionError) {
          setSubmitError(submissionError);
          return;
        }
        if (onCreated) {
          onCreated(result);
        } else {
          navigate(`/networking/virtual-networks/${result.id}`);
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
              {!!(submitError ?? error) && (
                <StackItem>
                  <Alert variant="danger" title={t('Failed to create virtual network')} isInline>
                    {getErrorMessage(submitError ?? error)}
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
