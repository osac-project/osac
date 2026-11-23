import { useEffect } from 'react';
import { MessageInitShape } from '@bufbuild/protobuf';
import {
  Alert,
  Button,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalVariant,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import {
  Protocol,
  type SecurityGroup,
  SecurityGroupSchema,
  SecurityGroups,
  type SecurityRule,
  SecurityRuleSchema,
} from '@osac/types';
import { intSchema } from '@osac/ui-components/validation/number';

import { type RuleFormValues, SecurityGroupRuleForm, showPortRange } from './SecurityGroupRuleForm';
import { useUpdateResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { buildCidrSchema } from '../../validation/cidr-validation';

const createRuleValidationSchema = (t: TFunction) =>
  Yup.lazy((values: RuleFormValues) => {
    const needsPorts = showPortRange(values.protocol);
    return Yup.object({
      protocol: Yup.number().required(t('Protocol is required')),
      portFrom: needsPorts
        ? intSchema(t, 1, 65535).required(t('Port From is required for TCP/UDP'))
        : Yup.mixed(),
      portTo: needsPorts
        ? intSchema(t, values.portFrom || 1, 65535).required(t('Port To is required for TCP/UDP'))
        : Yup.mixed(),
      ipv4Cidr: buildCidrSchema(t, 'ipv4').required(t('IPv4 CIDR is required')),
    });
  });

interface SecurityGroupRuleModalProps {
  onClose: () => void;
  securityGroup: SecurityGroup;
  direction: 'ingress' | 'egress';
  editingDisabledReason?: string;
  ruleIndex?: number;
}

export const SecurityGroupRuleModal = ({
  onClose,
  securityGroup,
  direction,
  editingDisabledReason,
  ruleIndex,
}: SecurityGroupRuleModalProps) => {
  const { t } = useTranslation();
  const updateSecurityGroup = useUpdateResource(SecurityGroups);

  const { error, isPending, mutateAsync } = updateSecurityGroup;
  const showRestrictedError = Boolean(editingDisabledReason && error);

  useEffect(() => {
    if (editingDisabledReason && !isPending && !error) {
      onClose();
    }
  }, [editingDisabledReason, error, isPending, onClose]);

  const mode: 'add' | 'edit' = ruleIndex === undefined ? 'add' : 'edit';
  const initialValues: SecurityRule | undefined =
    ruleIndex !== undefined ? securityGroup.spec?.[direction]?.[ruleIndex] : undefined;

  const defaultValues: RuleFormValues = {
    protocol: initialValues?.protocol ?? Protocol.TCP,
    portFrom: initialValues?.portFrom,
    portTo: initialValues?.portTo,
    ipv4Cidr: initialValues?.ipv4Cidr ?? '',
  };

  const handleSubmit = async (values: RuleFormValues) => {
    if (editingDisabledReason || isPending) {
      return;
    }

    const rule: MessageInitShape<typeof SecurityRuleSchema> = {
      protocol: values.protocol,
    };

    if (values.ipv4Cidr) {
      rule.ipv4Cidr = values.ipv4Cidr;
    }

    if (showPortRange(values.protocol)) {
      rule.portFrom = values.portFrom;
      rule.portTo = values.portTo;
    }

    const ingress: MessageInitShape<typeof SecurityRuleSchema>[] = [
      ...(securityGroup.spec?.ingress ?? []),
    ];
    const egress: MessageInitShape<typeof SecurityRuleSchema>[] = [
      ...(securityGroup.spec?.egress ?? []),
    ];
    const rules = direction === 'ingress' ? ingress : egress;

    if (ruleIndex === undefined) {
      rules.push(rule);
    } else {
      rules[ruleIndex] = rule;
    }

    const spec = { ingress, egress };

    const object: MessageInitShape<typeof SecurityGroupSchema> = {
      id: securityGroup.id,
      spec,
    };

    try {
      await mutateAsync({ object });
      onClose();
    } catch {
      // The modal displays the rejected mutation below.
    }
  };

  return (
    <Formik
      initialValues={defaultValues}
      validationSchema={createRuleValidationSchema(t)}
      onSubmit={handleSubmit}
    >
      {({ submitForm, isSubmitting, isValid }) => {
        const saving = isSubmitting || isPending;

        return (
          <Modal
            variant={ModalVariant.small}
            isOpen
            onClose={saving || showRestrictedError ? undefined : onClose}
            onEscapePress={!saving && showRestrictedError ? onClose : undefined}
            aria-label={mode === 'add' ? t('Add rule') : t('Edit rule')}
          >
            <ModalHeader title={mode === 'add' ? t('Add rule') : t('Edit rule')} />
            <ModalBody>
              {!!error && (
                <Alert variant="danger" title={t('Error')} isInline className="pf-v6-u-mb-md">
                  {getErrorMessage(error)}
                </Alert>
              )}
              {!showRestrictedError && <SecurityGroupRuleForm />}
            </ModalBody>
            <ModalFooter>
              {!showRestrictedError && (
                <Button
                  variant="primary"
                  onClick={() => submitForm()}
                  isDisabled={saving || Boolean(editingDisabledReason) || !isValid}
                  isLoading={saving}
                >
                  {mode === 'add' ? t('Add') : t('Save')}
                </Button>
              )}
              <Button variant="link" onClick={onClose} isDisabled={saving}>
                {showRestrictedError ? t('Close') : t('Cancel')}
              </Button>
            </ModalFooter>
          </Modal>
        );
      }}
    </Formik>
  );
};
