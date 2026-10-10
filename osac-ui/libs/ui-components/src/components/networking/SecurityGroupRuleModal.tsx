import { MessageInitShape } from '@bufbuild/protobuf';
import {
  Alert,
  Button,
  // eslint-disable-next-line no-restricted-imports
  Form,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalVariant,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { Protocol, type SecurityGroup, SecurityGroupSchema, type SecurityRule } from '@osac/types';

import { type RuleFormValues, SecurityGroupRuleForm } from './SecurityGroupRuleForm';
import { toPlainRule } from './securityGroupRuleUtils';
import { useUpdateSecurityGroup } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { buildCidrSchema } from '../../validation/cidr-validation';

const createRuleValidationSchema = (t: TFunction) =>
  Yup.object({
    protocol: Yup.number().required(t('Protocol is required')),
    portFrom: Yup.string().when('protocol', {
      is: (protocol: number) => protocol === Protocol.TCP || protocol === Protocol.UDP,
      then: (schema) =>
        schema
          .required(t('Port From is required for TCP/UDP'))
          .matches(/^\d+$/, t('Port must be a number'))
          .test('range', t('Port must be between 1 and 65535'), (value) => {
            if (!value) {
              return false;
            }
            const port = parseInt(value, 10);
            return port >= 1 && port <= 65535;
          }),
      otherwise: (schema) => schema.notRequired(),
    }),
    portTo: Yup.string().when('protocol', {
      is: (protocol: number) => protocol === Protocol.TCP || protocol === Protocol.UDP,
      then: (schema) =>
        schema
          .required(t('Port To is required for TCP/UDP'))
          .matches(/^\d+$/, t('Port must be a number'))
          .test('range', t('Port must be between 1 and 65535'), (value) => {
            if (!value) {
              return false;
            }
            const port = parseInt(value, 10);
            return port >= 1 && port <= 65535;
          })
          .test('min', t('Port To must be >= Port From'), function (value) {
            if (!value) {
              return false;
            }
            // eslint-disable-next-line @typescript-eslint/no-unsafe-assignment, @typescript-eslint/no-unsafe-member-access
            const portFrom = this.parent.portFrom;
            if (!portFrom) {
              return true;
            }
            // eslint-disable-next-line @typescript-eslint/no-unsafe-argument
            return parseInt(value, 10) >= parseInt(portFrom, 10);
          }),
      otherwise: (schema) => schema.notRequired(),
    }),
    ipv4Cidr: buildCidrSchema(t, 'ipv4').required(t('IPv4 CIDR is required')),
  });

interface SecurityGroupRuleModalProps {
  onClose: () => void;
  securityGroup: SecurityGroup;
  direction: 'ingress' | 'egress';
  ruleIndex?: number;
}

export const SecurityGroupRuleModal = ({
  onClose,
  securityGroup,
  direction,
  ruleIndex,
}: SecurityGroupRuleModalProps) => {
  const { t } = useTranslation();
  const updateSecurityGroup = useUpdateSecurityGroup();

  const mode: 'add' | 'edit' = ruleIndex === undefined ? 'add' : 'edit';
  const initialValues: SecurityRule | undefined =
    ruleIndex !== undefined ? securityGroup.spec?.[direction]?.[ruleIndex] : undefined;

  const defaultValues: RuleFormValues = {
    protocol: initialValues?.protocol ?? Protocol.TCP,
    portFrom: initialValues?.portFrom?.toString() ?? '',
    portTo: initialValues?.portTo?.toString() ?? '',
    ipv4Cidr: initialValues?.ipv4Cidr ?? '',
  };

  const handleSubmit = async (values: RuleFormValues) => {
    try {
      const rule = {
        protocol: values.protocol,
        ...(values.portFrom &&
          String(values.portFrom).trim() !== '' && {
            portFrom: parseInt(String(values.portFrom), 10),
          }),
        ...(values.portTo &&
          String(values.portTo).trim() !== '' && {
            portTo: parseInt(String(values.portTo), 10),
          }),
        ...(values.ipv4Cidr.trim() !== '' && { ipv4Cidr: values.ipv4Cidr }),
        ...(initialValues?.ipv6Cidr && { ipv6Cidr: initialValues.ipv6Cidr }),
      } as SecurityRule;

      const newIngress = (securityGroup.spec?.ingress ?? []).map(toPlainRule);
      const newEgress = (securityGroup.spec?.egress ?? []).map(toPlainRule);
      const targetList = direction === 'ingress' ? newIngress : newEgress;
      if (ruleIndex !== undefined) {
        targetList[ruleIndex] = rule;
      } else {
        targetList.push(rule);
      }

      const object: MessageInitShape<typeof SecurityGroupSchema> = {
        id: securityGroup.id,
        metadata: { name: securityGroup.metadata?.name ?? '' },
        spec: {
          virtualNetwork: securityGroup.spec?.virtualNetwork,
          ingress: newIngress,
          egress: newEgress,
        },
      };

      await updateSecurityGroup.mutateAsync({ object });
      onClose();
    } catch {
      // Error is surfaced via updateSecurityGroup.error below
    }
  };

  return (
    <Modal
      variant={ModalVariant.small}
      isOpen
      onClose={onClose}
      aria-label={mode === 'add' ? t('Add rule') : t('Edit rule')}
    >
      <ModalHeader title={mode === 'add' ? t('Add rule') : t('Edit rule')} />
      <Formik
        initialValues={defaultValues}
        validationSchema={createRuleValidationSchema(t)}
        onSubmit={handleSubmit}
      >
        {({ handleSubmit: formikSubmit, isValid }) => (
          <Form onSubmit={(e) => e.preventDefault()}>
            <ModalBody>
              {!!updateSecurityGroup.error && (
                <Alert
                  variant="danger"
                  title={t('Error')}
                  isInline
                  style={{ marginBottom: '1rem' }}
                >
                  {getErrorMessage(updateSecurityGroup.error)}
                </Alert>
              )}
              <SecurityGroupRuleForm />
            </ModalBody>
            <ModalFooter>
              <Button
                variant="primary"
                onClick={() => formikSubmit()}
                isDisabled={updateSecurityGroup.isPending || !isValid}
                isLoading={updateSecurityGroup.isPending}
              >
                {mode === 'add' ? t('Add') : t('Save')}
              </Button>
              <Button variant="link" onClick={onClose} isDisabled={updateSecurityGroup.isPending}>
                {t('Cancel')}
              </Button>
            </ModalFooter>
          </Form>
        )}
      </Formik>
    </Modal>
  );
};
