import { useNavigate } from 'react-router-dom';
import { MessageInitShape } from '@bufbuild/protobuf';
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
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import type { SecurityGroupSchema } from '@osac/types';

import { useCreateSecurityGroup, useVirtualNetworks } from '../../api/v1/networking';
import OsacForm from '../../components/Form/OsacForm';
import { SelectField } from '../../components/Form/SelectField';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import NameField from '../Form/NameField';

interface SecurityGroupCreateModalProps {
  onClose: () => void;
  /** Pre-select and lock the virtual network, e.g. when creating from a VN's detail page. */
  virtualNetworkId?: string;
}

interface FormValues {
  metadata: {
    name: string;
  };
  virtualNetwork: string;
}

const validationSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: Yup.string().required(t('Name is required')),
    }),
    virtualNetwork: Yup.string().required(t('Virtual network is required')),
  });

export const SecurityGroupCreateModal = ({
  onClose,
  virtualNetworkId,
}: SecurityGroupCreateModalProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const createSecurityGroup = useCreateSecurityGroup();
  const { data: virtualNetworks = [], isLoading, error: vnError } = useVirtualNetworks();

  const virtualNetworkOptions = virtualNetworks.map((vn) => ({
    value: vn.id,
    label: `${vn.metadata?.name ?? vn.id} (${vn.spec?.ipv4Cidr ?? ''})`,
  }));

  return (
    <Formik<FormValues>
      enableReinitialize
      initialValues={{
        metadata: {
          name: '',
        },
        virtualNetwork: virtualNetworkId || '',
      }}
      validationSchema={validationSchema(t)}
      onSubmit={async (values) => {
        try {
          const body: MessageInitShape<typeof SecurityGroupSchema> = {
            metadata: {
              name: values.metadata.name,
              project: virtualNetworks.find(({ id }) => id === values.virtualNetwork)?.metadata
                ?.project,
            },
            spec: {
              virtualNetwork: {
                id: values.virtualNetwork,
              },
              ingress: [],
              egress: [],
            },
          };
          const sg = await createSecurityGroup.mutateAsync(body);
          navigate(`/networking/security-groups/${sg.id}`);
        } catch {
          // Error is surfaced via createSecurityGroup.error below
        }
      }}
    >
      {({ submitForm, isSubmitting, isValid }) => (
        <Modal
          variant="medium"
          isOpen
          onClose={isSubmitting ? undefined : onClose}
          aria-labelledby="sg-create-modal-title"
        >
          <ModalHeader title={t('Create security group')} labelId="sg-create-modal-title" />
          <ModalBody>
            <Stack hasGutter>
              {!!createSecurityGroup.error && (
                <StackItem>
                  <Alert variant="danger" title={t('Error')} isInline>
                    {getErrorMessage(createSecurityGroup.error)}
                  </Alert>
                </StackItem>
              )}

              {!!vnError && (
                <StackItem>
                  <Alert variant="danger" title={t('Error loading virtual networks')} isInline>
                    {getErrorMessage(vnError)}
                  </Alert>
                </StackItem>
              )}
              <StackItem>
                <OsacForm>
                  <SelectField
                    name="virtualNetwork"
                    label={t('Virtual Network')}
                    fieldId="sg-vn"
                    isRequired
                    isLoading={isLoading}
                    isDisabled={Boolean(virtualNetworkId)}
                    placeholder={t('Select a virtual network')}
                    options={virtualNetworkOptions}
                  />
                  <NameField />
                </OsacForm>
              </StackItem>
            </Stack>
          </ModalBody>
          <ModalFooter>
            <Button
              variant="primary"
              onClick={submitForm}
              isLoading={isSubmitting}
              isDisabled={isSubmitting || !isValid || isLoading}
            >
              {t('Create')}
            </Button>
            <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
              {t('Cancel')}
            </Button>
          </ModalFooter>
        </Modal>
      )}
    </Formik>
  );
};
