import { useMemo } from 'react';
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

import type { Subnet, VirtualNetwork } from '@osac/types';
import { useCreateSubnet } from '@osac/ui-components/api/v1/networking';

import { InputField } from '../../components/Form/InputField';
import OsacForm from '../../components/Form/OsacForm';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import {
  buildCidrSchema,
  hasSubnetOverlap,
  isSubnetWithinVN,
} from '../../validation/cidr-validation';
import NameField from '../Form/NameField';

interface SubnetCreateModalProps {
  onClose: () => void;
  parentVN: VirtualNetwork;
  existingSubnets: Subnet[];
}

export const SubnetCreateModal = ({
  onClose,
  parentVN,
  existingSubnets,
}: SubnetCreateModalProps) => {
  const { mutateAsync: create, error } = useCreateSubnet();
  const { t } = useTranslation();

  const parentIPv4CIDR = parentVN.spec?.ipv4Cidr ?? '';
  const hasIPv4 = Boolean(parentIPv4CIDR);

  const validationSchema = useMemo(
    () =>
      Yup.object({
        metadata: Yup.object({
          name: Yup.string().required(t('Name is required')),
        }),
        ipv4Cidr: hasIPv4
          ? buildCidrSchema(t, 'ipv4')
              .required(t('IPv4 CIDR is required'))
              .test('within-vn', t('CIDR must be within parent virtual network range'), (value) => {
                if (!value || !parentIPv4CIDR) {
                  return true;
                }
                return isSubnetWithinVN(value, parentIPv4CIDR);
              })
              .test('no-overlap', function (value) {
                if (!value) {
                  return true;
                }
                const overlappingSubnet = existingSubnets.find((s) => {
                  const existingCidr = s.spec?.ipv4Cidr;
                  return existingCidr && hasSubnetOverlap(value, [existingCidr]);
                });
                if (overlappingSubnet) {
                  const subnetName = overlappingSubnet.metadata?.name || overlappingSubnet.id;
                  const subnetCidr = overlappingSubnet.spec?.ipv4Cidr;
                  return this.createError({
                    message: t('CIDR overlaps with existing subnet "{{name}}" ({{cidr}})', {
                      name: subnetName,
                      cidr: subnetCidr,
                    }),
                  });
                }
                return true;
              })
          : Yup.string(),
      }),
    [t, hasIPv4, parentIPv4CIDR, existingSubnets],
  );

  return (
    <Formik
      initialValues={{ metadata: { name: '' }, ipv4Cidr: '' }}
      validationSchema={validationSchema}
      onSubmit={async (values) => {
        try {
          await create({
            metadata: { name: values.metadata.name, project: parentVN.metadata?.project },
            spec: {
              virtualNetwork: {
                name: parentVN.metadata?.name,
              },
              ...(values.ipv4Cidr && { ipv4Cidr: values.ipv4Cidr }),
            },
          });
          onClose();
        } catch {
          // tanstack handles the error
        }
      }}
    >
      {({ handleSubmit, isSubmitting }) => (
        <Modal
          variant="small"
          isOpen
          onClose={isSubmitting ? undefined : onClose}
          aria-labelledby="subnet-create-modal-title"
        >
          <ModalHeader title={t('Create subnet')} labelId="subnet-create-modal-title" />
          <ModalBody>
            <Stack hasGutter>
              <StackItem>
                <OsacForm>
                  <p>
                    {t('Parent virtual network')}: <strong>{parentVN.metadata?.name}</strong>
                  </p>
                  <p>
                    {t('Parent virtual network CIDR')}: {parentIPv4CIDR || '—'}
                  </p>
                  <NameField />
                  {hasIPv4 && (
                    <InputField
                      name="ipv4Cidr"
                      label={t('IPv4 CIDR')}
                      fieldId="subnet-ipv4-cidr"
                      placeholder="10.0.1.0/24"
                      isRequired
                    />
                  )}
                </OsacForm>
              </StackItem>
              {!!error && (
                <StackItem>
                  <Alert variant="danger" title={t('Failed to create subnet')} isInline>
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
              onClick={() => handleSubmit()}
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
