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
import { Formik, useField } from 'formik';
import * as Yup from 'yup';

import type { VirtualNetwork } from '@osac/types';

import { useCreateVirtualNetwork } from '../../api/v1/networking';
import { InputField } from '../../components/Form/InputField';
import OsacForm from '../../components/Form/OsacForm';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { buildCidrSchema } from '../../validation/cidr-validation';
import NameField from '../Form/NameField';
import ProjectField from '../Form/ProjectField';
import { type ResourceSelectValue, emptyResourceSelectValue } from '../Form/resourceSelectValue';
import TenantSelectField from '../Form/TenantSelectField';

type VirtualNetworkCreateFormValues = {
  metadata: {
    name: string;
    project: string;
    tenant: ResourceSelectValue;
  };
  ipv4Cidr: string;
};

const VirtualNetworkCreateForm = ({ isAdmin }: { isAdmin: boolean }) => {
  const { t } = useTranslation();
  const [tenantField] = useField<ResourceSelectValue>('metadata.tenant');
  const [, , projectHelpers] = useField<string>('metadata.project');
  const tenant = tenantField.value?.id ?? '';

  return (
    <OsacForm>
      {isAdmin && <TenantSelectField onSelect={() => void projectHelpers.setValue('', false)} />}
      <ProjectField tenant={isAdmin ? tenant : undefined} isDisabled={isAdmin && !tenant} />
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
  const { role } = useSession();
  const isAdmin = role === 'admin';
  const { mutateAsync: create, error } = useCreateVirtualNetwork();

  const validationSchema = useMemo(
    () =>
      Yup.object({
        metadata: Yup.object({
          name: Yup.string().required(t('Name is required')),
          tenant: isAdmin
            ? Yup.object({ id: Yup.string().required(t('Tenant is required')) })
            : Yup.mixed().notRequired(),
        }),
        ipv4Cidr: buildCidrSchema(t, 'ipv4').required(t('IPv4 CIDR is required')),
      }),
    [isAdmin, t],
  );

  return (
    <Formik<VirtualNetworkCreateFormValues>
      initialValues={{
        metadata: { name: '', project: initialProject, tenant: emptyResourceSelectValue() },
        ipv4Cidr: '',
      }}
      validationSchema={validationSchema}
      onSubmit={async (values) => {
        let result: VirtualNetwork;
        try {
          result = await create({
            metadata: {
              name: values.metadata.name,
              project: values.metadata.project,
              ...(isAdmin && values.metadata.tenant.id
                ? { tenant: values.metadata.tenant.id }
                : {}),
            },
            spec: {
              ipv4Cidr: values.ipv4Cidr || undefined,
            },
          });
        } catch {
          // tanstack handles the error
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
                <VirtualNetworkCreateForm isAdmin={isAdmin} />
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
