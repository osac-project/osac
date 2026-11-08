import { Button, Flex, FlexItem, FormSection, Stack, StackItem } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, useFormikContext } from 'formik';

import { Protocol } from '@osac/types';

import { protocolToString } from './SecurityGroupRulesTable';
import {
  type SecurityGroupRulesFormValues,
  emptyRuleFormValues,
  usesPortRange,
} from './securityGroupRuleUtils';
import { useTranslation } from '../../hooks/useTranslation';
import TextField from '../Form/base/TextField';
import { InputField } from '../Form/InputField';
import { SelectField } from '../Form/SelectField';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface SecurityGroupRuleRowsProps {
  direction: 'ingress' | 'egress';
  isDisabled?: boolean;
}

/** The editable rule rows of one direction of the Edit rules form. */
const SecurityGroupRuleRows = ({ direction, isDisabled = false }: SecurityGroupRuleRowsProps) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<SecurityGroupRulesFormValues>();
  const rules = values[direction];
  const cidrLabel = direction === 'ingress' ? t('Source IPv4 CIDR') : t('Destination IPv4 CIDR');

  // The UI does not offer ICMP: the API rejects it when a network manager lacks the ICMP
  // capability, and All includes it. A stored ICMP rule keeps its option.
  const protocolOptions = (protocol: Protocol) => [
    { value: Protocol.TCP, label: protocolToString(Protocol.TCP, t) },
    { value: Protocol.UDP, label: protocolToString(Protocol.UDP, t) },
    ...(protocol === Protocol.ICMP
      ? [{ value: Protocol.ICMP, label: protocolToString(Protocol.ICMP, t) }]
      : []),
    { value: Protocol.ALL, label: protocolToString(Protocol.ALL, t) },
  ];

  return (
    <FormSection title={direction === 'ingress' ? t('Inbound rules') : t('Outbound rules')}>
      <FieldArray name={direction}>
        {(helpers) => (
          <Stack hasGutter>
            {rules.length === 0 ? (
              <StackItem>
                <SubtleContent component="p">
                  {direction === 'ingress'
                    ? t('No inbound rules. Incoming traffic is blocked.')
                    : t('No outbound rules. Outgoing traffic is blocked.')}
                </SubtleContent>
              </StackItem>
            ) : null}
            {rules.map((rule, index) => {
              const fieldId = `${direction}-${index}`;
              return (
                <StackItem key={index}>
                  <Flex
                    alignItems={{ default: 'alignItemsFlexStart' }}
                    spaceItems={{ default: 'spaceItemsMd' }}
                    flexWrap={{ default: 'wrap' }}
                  >
                    <FlexItem>
                      <SelectField
                        name={`${direction}.${index}.protocol`}
                        label={t('Protocol')}
                        fieldId={`${fieldId}-protocol`}
                        isRequired
                        isDisabled={isDisabled}
                        options={protocolOptions(rule.protocol)}
                      />
                    </FlexItem>
                    {usesPortRange(rule.protocol) ? (
                      <>
                        <FlexItem>
                          <InputField
                            name={`${direction}.${index}.portFrom`}
                            label={t('Port from')}
                            fieldId={`${fieldId}-port-from`}
                            type="number"
                            isRequired
                            isDisabled={isDisabled}
                          />
                        </FlexItem>
                        <FlexItem>
                          <InputField
                            name={`${direction}.${index}.portTo`}
                            label={t('Port to')}
                            fieldId={`${fieldId}-port-to`}
                            type="number"
                            isRequired
                            isDisabled={isDisabled}
                          />
                        </FlexItem>
                      </>
                    ) : null}
                    <FlexItem>
                      <TextField
                        name={`${direction}.${index}.ipv4Cidr`}
                        label={cidrLabel}
                        fieldId={`${fieldId}-ipv4-cidr`}
                        isRequired
                        isDisabled={isDisabled}
                        placeholder="192.168.1.0/24"
                      />
                    </FlexItem>
                    <FlexItem alignSelf={{ default: 'alignSelfFlexEnd' }}>
                      <Button
                        variant="link"
                        icon={<MinusCircleIcon />}
                        onClick={() => helpers.remove(index)}
                        isDisabled={isDisabled}
                        aria-label={t('Remove rule {{number}}', { number: index + 1 })}
                      >
                        {t('Remove')}
                      </Button>
                    </FlexItem>
                  </Flex>
                </StackItem>
              );
            })}
            <StackItem>
              <Button
                variant="link"
                isInline
                icon={<PlusCircleIcon />}
                onClick={() => helpers.push(emptyRuleFormValues())}
                isDisabled={isDisabled}
              >
                {t('Add rule')}
              </Button>
            </StackItem>
          </Stack>
        )}
      </FieldArray>
    </FormSection>
  );
};

export default SecurityGroupRuleRows;
