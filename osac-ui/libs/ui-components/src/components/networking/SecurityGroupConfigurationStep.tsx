import {
  Button,
  FormFieldGroup,
  FormFieldGroupHeader,
  FormSection,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import { Protocol } from '@osac/types';

import { InputField } from '../../components/Form/InputField';
import OsacForm from '../../components/Form/OsacForm';
import { SelectField } from '../../components/Form/SelectField';
import { useTranslation } from '../../hooks/useTranslation';

interface SecurityGroupConfigurationStepProps {
  virtualNetworkOptions: { value: string; label: string }[];
  isVirtualNetworkLoading: boolean;
  isVirtualNetworkDisabled: boolean;
}

export interface RuleFormValues {
  protocol: Protocol;
  portFrom: string;
  portTo: string;
  ipv4Cidr: string;
}

export interface SecurityGroupFormValues {
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
    <FormSection title={title} titleElement="h2">
      <FieldArray name={name}>
        {(helpers) => (
          <Stack hasGutter>
            {rules.length === 0 ? <StackItem>{t('No rules added.')}</StackItem> : null}
            {rules.map((_rule, index) => (
              <StackItem key={index}>
                <FormFieldGroup
                  header={
                    <FormFieldGroupHeader
                      titleText={{ text: t('Rule {{number}}', { number: index + 1 }), id: `${name}-rule-${index}` }}
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
                  <SelectField name={`${name}.${index}.protocol`} label={t('Protocol')} fieldId={`${name}-rule-${index}-protocol`} isRequired options={protocolOptions} />
                  <InputField name={`${name}.${index}.portFrom`} label={t('Port From')} fieldId={`${name}-rule-${index}-from`} type="number" />
                  <InputField name={`${name}.${index}.portTo`} label={t('Port To')} fieldId={`${name}-rule-${index}-to`} type="number" />
                  <InputField name={`${name}.${index}.ipv4Cidr`} label={t('IPv4 CIDR')} fieldId={`${name}-rule-${index}-ipv4`} />
                </FormFieldGroup>
              </StackItem>
            ))}
            <StackItem>
              <Button variant="link" icon={<PlusCircleIcon />} onClick={() => helpers.push(emptyRule())}>
                {t('Add rule')}
              </Button>
            </StackItem>
          </Stack>
        )}
      </FieldArray>
    </FormSection>
  );
};

const SecurityGroupConfigurationStep = ({
  virtualNetworkOptions,
  isVirtualNetworkLoading,
  isVirtualNetworkDisabled,
}: SecurityGroupConfigurationStepProps) => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Security group rules')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          <SelectField
            name="virtualNetwork"
            label={t('Virtual Network')}
            fieldId="sg-vn"
            isRequired
            isLoading={isVirtualNetworkLoading}
            isDisabled={isVirtualNetworkDisabled}
            placeholder={t('Select a virtual network')}
            options={virtualNetworkOptions}
          />
          <Stack hasGutter>
            <RuleArrayField name="ingress" title={t('Inbound rules')} />
            <RuleArrayField name="egress" title={t('Outbound rules')} />
          </Stack>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default SecurityGroupConfigurationStep;
