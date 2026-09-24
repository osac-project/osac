import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { MessageInitShape } from '@bufbuild/protobuf';
import {
  Alert,
  Button,
  FormFieldGroup,
  FormFieldGroupHeader,
  Modal,
  ModalBody,
  ModalHeader,
  Stack,
  StackItem,
  Wizard,
  WizardStep,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, Formik, useFormikContext } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { Protocol, SecurityRuleSchema } from '@osac/types';

import { useCreateSecurityGroup, useVirtualNetworks } from '../../api/v1/networking';
import { InputField } from '../../components/Form/InputField';
import OsacForm from '../../components/Form/OsacForm';
import ProjectField from '../../components/Form/ProjectField';
import { SelectField } from '../../components/Form/SelectField';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import NameField from '../catalogProvision/wizard/fields/NameField';

interface SecurityGroupCreateModalProps {
  onClose: () => void;
  virtualNetworkId?: string;
}

interface RuleFormValues {
  protocol: Protocol;
  portFrom: string;
  portTo: string;
  ipv4Cidr: string;
}

interface SecurityGroupFormValues {
  metadata: { name: string; project: string; description: string };
  virtualNetwork: string;
  ingress: RuleFormValues[];
  egress: RuleFormValues[];
}

const emptyRule = (): RuleFormValues => ({
  protocol: Protocol.ALL,
  portFrom: '',
  portTo: '',
  ipv4Cidr: '',
});

const ruleSchema = (t: TFunction) =>
  Yup.object({
    protocol: Yup.number().required(),
    portFrom: Yup.string().when('protocol', {
      is: (protocol: Protocol) => protocol === Protocol.TCP || protocol === Protocol.UDP,
      then: (schema) => schema.required(t('Port From is required for TCP/UDP')),
    }),
    portTo: Yup.string().when('protocol', {
      is: (protocol: Protocol) => protocol === Protocol.TCP || protocol === Protocol.UDP,
      then: (schema) => schema.required(t('Port To is required for TCP/UDP')),
    }),
  });

const validationSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: Yup.string().required(t('Name is required')),
      project: Yup.string().required(t('Project is required')),
      description: Yup.string(),
    }),
    virtualNetwork: Yup.string().required(t('Virtual network is required')),
    ingress: Yup.array().of(ruleSchema(t)),
    egress: Yup.array().of(ruleSchema(t)),
  });

const toSecurityRule = (rule: RuleFormValues): MessageInitShape<typeof SecurityRuleSchema> => ({
  protocol: rule.protocol,
  portFrom: rule.portFrom ? Number(rule.portFrom) : undefined,
  portTo: rule.portTo ? Number(rule.portTo) : undefined,
  ipv4Cidr: rule.ipv4Cidr || undefined,
});

const RuleArrayField = ({ name, title }: { name: 'ingress' | 'egress'; title: string }) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<SecurityGroupFormValues>();
  const rules = values[name];
  const protocolOptions = [
    { value: Protocol.TCP, label: t('TCP') },
    { value: Protocol.UDP, label: t('UDP') },
    { value: Protocol.ICMP, label: t('ICMP') },
    { value: Protocol.ALL, label: t('All') },
  ];

  return (
    <FormFieldGroup
      header={<FormFieldGroupHeader titleText={{ text: title, id: `${name}-rules` }} />}
    >
      <FieldArray name={name}>
        {(helpers) => (
          <Stack hasGutter>
            {rules.length === 0 ? <StackItem>{t('No rules added.')}</StackItem> : null}
            {rules.map((_rule, index) => (
              <StackItem key={index}>
                <FormFieldGroup
                  header={
                    <FormFieldGroupHeader
                      titleText={{
                        text: t('Rule {{number}}', { number: index + 1 }),
                        id: `${name}-rule-${index}`,
                      }}
                      actions={
                        <Button
                          variant="plain"
                          aria-label={t('Remove rule {{number}}', { number: index + 1 })}
                          onClick={() => helpers.remove(index)}
                          icon={<MinusCircleIcon />}
                        />
                      }
                    />
                  }
                >
                  <SelectField
                    name={`${name}.${index}.protocol`}
                    label={t('Protocol')}
                    fieldId={`${name}-rule-${index}-protocol`}
                    isRequired
                    options={protocolOptions}
                  />
                  <InputField
                    name={`${name}.${index}.portFrom`}
                    label={t('Port From')}
                    fieldId={`${name}-rule-${index}-from`}
                    type="number"
                  />
                  <InputField
                    name={`${name}.${index}.portTo`}
                    label={t('Port To')}
                    fieldId={`${name}-rule-${index}-to`}
                    type="number"
                  />
                  <InputField
                    name={`${name}.${index}.ipv4Cidr`}
                    label={t('IPv4 CIDR')}
                    fieldId={`${name}-rule-${index}-ipv4`}
                  />
                </FormFieldGroup>
              </StackItem>
            ))}
            <StackItem>
              <Button
                variant="link"
                icon={<PlusCircleIcon />}
                onClick={() => helpers.push(emptyRule())}
              >
                {t('Add rule')}
              </Button>
            </StackItem>
          </Stack>
        )}
      </FieldArray>
    </FormFieldGroup>
  );
};

export const SecurityGroupCreateModal = ({
  onClose,
  virtualNetworkId,
}: SecurityGroupCreateModalProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [step, setStep] = useState('general');
  const createSecurityGroup = useCreateSecurityGroup();
  const { data: virtualNetworks = [], isLoading, error: vnError } = useVirtualNetworks();

  const virtualNetworkOptions = virtualNetworks.map((vn) => ({
    value: vn.id,
    label: `${vn.metadata?.name ?? vn.id} (${vn.spec?.ipv4Cidr ?? ''})`,
  }));

  return (
    <Formik<SecurityGroupFormValues>
      initialValues={{
        metadata: { name: '', project: '', description: '' },
        virtualNetwork: virtualNetworkId || '',
        ingress: [],
        egress: [],
      }}
      validationSchema={validationSchema(t)}
      onSubmit={async (values) => {
        try {
          const sg = await createSecurityGroup.mutateAsync({
            metadata: values.metadata,
            spec: {
              virtualNetwork: { id: values.virtualNetwork },
              ingress: values.ingress.map(toSecurityRule),
              egress: values.egress.map(toSecurityRule),
            },
          });
          navigate(`/networking/security-groups/${sg.id}`);
        } catch {
          // Surfaced through the mutation error below.
        }
      }}
    >
      {({ submitForm, validateForm, isSubmitting, setFieldTouched }) => (
        <Modal
          variant="large"
          isOpen
          onClose={isSubmitting ? undefined : onClose}
          aria-labelledby="sg-create-modal-title"
        >
          <ModalHeader title={t('Create security group')} labelId="sg-create-modal-title" />
          <ModalBody>
            <Wizard
              navAriaLabel={t('Create security group steps')}
              onStepChange={(_, nextStep) => setStep(String(nextStep.id))}
              footer={
                <Stack>
                  <StackItem>
                    <Button
                      variant="primary"
                      onClick={async () => {
                        const errors = await validateForm();
                        if (step === 'general' && (errors.metadata || errors.virtualNetwork)) {
                          setFieldTouched('metadata.name', true);
                          setFieldTouched('metadata.project', true);
                          setFieldTouched('virtualNetwork', true);
                        } else if (step === 'configuration' && (errors.ingress || errors.egress)) {
                          setFieldTouched('ingress', true);
                          setFieldTouched('egress', true);
                        } else if (step === 'configuration') {
                          await submitForm();
                        } else {
                          setStep('configuration');
                        }
                      }}
                      isLoading={isSubmitting}
                    >
                      {step === 'configuration' ? t('Create') : t('Next')}
                    </Button>
                    <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
                      {t('Cancel')}
                    </Button>
                  </StackItem>
                </Stack>
              }
            >
              <WizardStep id="general" name={t('General')}>
                {step === 'general' && (
                  <OsacForm>
                    <ProjectField />
                    <NameField />
                    <InputField
                      name="metadata.description"
                      label={t('Description')}
                      fieldId="sg-description"
                      multiline
                    />
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
                  </OsacForm>
                )}
              </WizardStep>
              <WizardStep id="configuration" name={t('Configuration')}>
                {step === 'configuration' && (
                  <Stack hasGutter>
                    <RuleArrayField name="ingress" title={t('Inbound rules')} />
                    <RuleArrayField name="egress" title={t('Outbound rules')} />
                  </Stack>
                )}
              </WizardStep>
            </Wizard>
            {!!(createSecurityGroup.error || vnError) && (
              <Alert variant="danger" title={t('Error')} isInline>
                {String(getErrorMessage(createSecurityGroup.error ?? vnError))}
              </Alert>
            )}
          </ModalBody>
        </Modal>
      )}
    </Formik>
  );
};
