import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import type { TFunction } from 'i18next';

import { Protocol } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { SubtleContent } from '../SubtleContent/SubtleContent';

/** A rule of the public or private API; both share the field numbers and protocol values. */
export interface DisplayedSecurityRule {
  protocol: number;
  portFrom?: number;
  portTo?: number;
  ipv4Cidr?: string;
}

interface SecurityGroupRulesTableProps {
  rules: DisplayedSecurityRule[];
  /** Overrides the text shown when there are no rules. */
  emptyText?: string;
  direction: 'ingress' | 'egress';
}

export const protocolToString = (protocol: Protocol, t: TFunction): string => {
  switch (protocol) {
    case Protocol.TCP:
      return t('TCP');
    case Protocol.UDP:
      return t('UDP');
    case Protocol.ICMP:
      return t('ICMP');
    case Protocol.ALL:
      return t('All');
    default:
      return t('Unknown');
  }
};

const formatPortRange = (portFrom?: number, portTo?: number): string => {
  if (portFrom === undefined && portTo === undefined) {
    return '—';
  }
  if (portFrom === portTo) {
    return String(portFrom);
  }
  return `${portFrom ?? '—'}-${portTo ?? '—'}`;
};

const formatCidr = (ipv4Cidr?: string): string => ipv4Cidr || '—';

/** The read-only rules of one direction; rules change only through Edit rules. */
export const SecurityGroupRulesTable = ({
  rules,
  direction,
  emptyText,
}: SecurityGroupRulesTableProps) => {
  const { t } = useTranslation();
  const cidrLabel = direction === 'ingress' ? t('Source CIDR') : t('Destination CIDR');

  if (rules.length === 0) {
    return (
      <SubtleContent component="p">
        {emptyText ??
          (direction === 'ingress'
            ? t('No inbound rules. Use Edit rules to allow incoming traffic.')
            : t('No outbound rules. Use Edit rules to allow outgoing traffic.'))}
      </SubtleContent>
    );
  }

  return (
    <Table
      aria-label={direction === 'ingress' ? t('Inbound rules') : t('Outbound rules')}
      variant="compact"
    >
      <Thead>
        <Tr>
          <Th>{t('Protocol')}</Th>
          <Th>{t('Port range')}</Th>
          <Th>{cidrLabel}</Th>
        </Tr>
      </Thead>
      <Tbody>
        {rules.map((rule, index) => (
          <Tr key={index}>
            <Td dataLabel={t('Protocol')}>{protocolToString(rule.protocol as Protocol, t)}</Td>
            <Td dataLabel={t('Port range')}>{formatPortRange(rule.portFrom, rule.portTo)}</Td>
            <Td dataLabel={cidrLabel}>{formatCidr(rule.ipv4Cidr)}</Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
