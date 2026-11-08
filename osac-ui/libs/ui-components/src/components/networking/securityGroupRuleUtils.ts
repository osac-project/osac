import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { Protocol, type SecurityGroup, SecurityGroupState, type SecurityRule } from '@osac/types';

import { buildCidrSchema } from '../../validation/cidr-validation';

/** The status message of a READY SecurityGroup whose rule update is running. */
export const SECURITY_GROUP_UPDATING_MESSAGE = 'Updating';

/** Whether a READY SecurityGroup is applying a rule update. */
export const isSecurityGroupUpdating = (securityGroup?: SecurityGroup): boolean =>
  securityGroup?.status?.state === SecurityGroupState.READY &&
  securityGroup.status?.message === SECURITY_GROUP_UPDATING_MESSAGE;

/**
 * The status message for the popover of a SecurityGroup status label; none while the
 * label already says Updating.
 */
export const securityGroupStatusMessage = (securityGroup?: SecurityGroup): string | undefined =>
  isSecurityGroupUpdating(securityGroup) ? undefined : securityGroup?.status?.message;

/**
 * Why the rules of a SecurityGroup cannot be edited yet, or undefined. The server
 * rejects an update of a PENDING or updating group and of a group being deleted.
 * A FAILED group stays editable so its rules can be fixed. Automatic groups never
 * allow rule edits; callers hide the actions for them (isAutoCreatedResource).
 */
export const securityGroupRuleEditDisabledReason = (
  t: TFunction,
  securityGroup?: SecurityGroup,
): string | undefined => {
  if (!securityGroup) {
    return undefined;
  }
  const state = securityGroup.status?.state;
  if (state === SecurityGroupState.DELETING) {
    return t('Rules cannot be edited while the security group is being deleted.');
  }
  if (state === SecurityGroupState.PENDING || isSecurityGroupUpdating(securityGroup)) {
    return t('Available when the security group is Ready or Failed.');
  }
  return undefined;
};

/** A rule row of the Edit rules form. Ports stay strings while the user types. */
export interface RuleFormValues {
  protocol: Protocol;
  portFrom: string;
  portTo: string;
  ipv4Cidr: string;
}

export interface SecurityGroupRulesFormValues {
  ingress: RuleFormValues[];
  egress: RuleFormValues[];
}

export const usesPortRange = (protocol: Protocol): boolean =>
  protocol === Protocol.TCP || protocol === Protocol.UDP;

export const toRuleFormValues = (rule: SecurityRule): RuleFormValues => ({
  protocol: rule.protocol,
  portFrom: rule.portFrom?.toString() ?? '',
  portTo: rule.portTo?.toString() ?? '',
  ipv4Cidr: rule.ipv4Cidr ?? '',
});

export const emptyRuleFormValues = (): RuleFormValues => ({
  protocol: Protocol.TCP,
  portFrom: '',
  portTo: '',
  ipv4Cidr: '',
});

/** The rule as sent in SecurityGroups.Update; ports only for TCP and UDP. */
export const fromRuleFormValues = (values: RuleFormValues) => ({
  protocol: values.protocol,
  ...(usesPortRange(values.protocol) && {
    portFrom: parseInt(values.portFrom, 10),
    portTo: parseInt(values.portTo, 10),
  }),
  ...(values.ipv4Cidr.trim() !== '' && { ipv4Cidr: values.ipv4Cidr.trim() }),
});

const portSchema = (t: TFunction, requiredMessage: string) =>
  Yup.string()
    .required(requiredMessage)
    .matches(/^\d+$/, t('Port must be a number'))
    .test('range', t('Port must be between 1 and 65535'), (value) => {
      const port = parseInt(value ?? '', 10);
      return port >= 1 && port <= 65535;
    });

/** Marks an ICMP rule that the server rejected; the message tells the user what to use. */
export const icmpRejectedMessage = (t: TFunction) =>
  t(
    'The network does not support ICMP rules. Remove the rule, or use All, which also allows TCP and UDP on every port.',
  );

/** Whether a rejected update names ICMP, so the form can mark the ICMP rows. */
export const isIcmpRejection = (message: string): boolean => /\bicmp\b/i.test(message);

interface RulesSchemaOptions {
  /** Set after the server rejected ICMP: ICMP rows are marked until changed. */
  rejectIcmp?: boolean;
}

const ruleSchema = (t: TFunction, { rejectIcmp = false }: RulesSchemaOptions) =>
  Yup.object({
    protocol: Yup.number()
      .required(t('Protocol is required'))
      .test('icmp', icmpRejectedMessage(t), (value) => !rejectIcmp || value !== Protocol.ICMP),
    portFrom: Yup.string().when('protocol', {
      is: usesPortRange,
      then: () => portSchema(t, t('Port from is required for TCP and UDP')),
      otherwise: (schema) => schema.notRequired(),
    }),
    portTo: Yup.string().when('protocol', {
      is: usesPortRange,
      then: () =>
        portSchema(t, t('Port to is required for TCP and UDP')).test(
          'min',
          t('Port to must be greater than or equal to Port from'),
          function (value) {
            const portFrom = (this.parent as RuleFormValues).portFrom;
            if (!value || !portFrom) {
              return true;
            }
            return parseInt(value, 10) >= parseInt(portFrom, 10);
          },
        ),
      otherwise: (schema) => schema.notRequired(),
    }),
    ipv4Cidr: buildCidrSchema(t, 'ipv4').required(t('IPv4 CIDR is required')),
  });

export const securityGroupRulesSchema = (t: TFunction, options: RulesSchemaOptions = {}) =>
  Yup.object({
    ingress: Yup.array().of(ruleSchema(t, options)),
    egress: Yup.array().of(ruleSchema(t, options)),
  });
