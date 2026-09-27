import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { MessageInitShape } from '@bufbuild/protobuf';
import { Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';
import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { Protocol, SecurityRuleSchema } from '@osac/types';

import SecurityGroupConfigurationStep, {
  type RuleFormValues,
  type SecurityGroupFormValues,
} from './SecurityGroupConfigurationStep';
import { useCreateSecurityGroup, useVirtualNetworks } from '../../api/v1/networking';
import { InputField } from '../../components/Form/InputField';
import OsacForm from '../../components/Form/OsacForm';
import ProjectField from '../../components/Form/ProjectField';
import { useTranslation } from '../../hooks/useTranslation';
import NameField from '../catalogProvision/wizard/fields/NameField';
import { OSACWizardFooter } from '../Wizard/OSACWizardFooter';

interface SecurityGroupCreateWizardProps {
  virtualNetworkId?: string;
}

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
      project: Yup.string(),
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

const securityGroupStepHasErrors = (
  stepId: string,
  errors: FormikErrors<SecurityGroupFormValues>,
) => {
  if (stepId === 'general') {
    return Boolean(errors.metadata?.name);
  }
  if (stepId === 'configuration') {
    return Boolean(errors.virtualNetwork || errors.ingress || errors.egress);
  }
  return false;
};

export const SecurityGroupCreateWizard = ({
  virtualNetworkId,
}: SecurityGroupCreateWizardProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [step, setStep] = useState('general');
  const {
    mutateAsync: createSecurityGroup,
    error: createError,
    reset: resetCreate = () => undefined,
  } = useCreateSecurityGroup();
  const { data: virtualNetworks = [], isLoading, error: vnError } = useVirtualNetworks();
  const handleErrorReset = useCallback(() => resetCreate(), [resetCreate]);

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
          const sg = await createSecurityGroup({
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
      {() => (
        <>
          <Wizard
            navAriaLabel={t('Create security group steps')}
            isVisitRequired
            onStepChange={(_, nextStep) => setStep(String(nextStep.id))}
            footer={
              <OSACWizardFooter
                onCancel={() => navigate('/networking/security-groups')}
                stepHasErrors={securityGroupStepHasErrors}
                error={createError || vnError}
                onErrorReset={handleErrorReset}
              />
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
                  </OsacForm>
                )}
              </WizardStep>
              <WizardStep id="configuration" name={t('Configuration')}>
                {step === 'configuration' && (
                  <SecurityGroupConfigurationStep
                    virtualNetworkOptions={virtualNetworkOptions}
                    isVirtualNetworkLoading={isLoading}
                    isVirtualNetworkDisabled={Boolean(virtualNetworkId)}
                  />
                )}
              </WizardStep>
          </Wizard>
        </>
      )}
    </Formik>
  );
};
