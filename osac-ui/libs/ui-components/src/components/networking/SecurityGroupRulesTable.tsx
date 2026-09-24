import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import type { TFunction } from 'i18next';

import { Protocol, type SecurityRule } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface SecurityGroupRulesTableProps {
  rules: SecurityRule[];
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

const formatCidr = (ipv4Cidr?: string, ipv6Cidr?: string): string => {
  const cidrs = [ipv4Cidr, ipv6Cidr].filter(Boolean);
  return cidrs.length > 0 ? cidrs.join(', ') : '—';
};

export const SecurityGroupRulesTable = ({ rules, direction }: SecurityGroupRulesTableProps) => {
  const { t } = useTranslation();

  const cidrLabel = direction === 'ingress' ? t('Source CIDR') : t('Destination CIDR');

  if (rules.length === 0) {
    return (
      <SubtleContent component="p">
        {direction === 'ingress' ? t('No inbound rules.') : t('No outbound rules.')}
      </SubtleContent>
    );
  }

  return (
    <div>
      <Table aria-label={`${direction} rules`} variant="compact" borders>
        <Thead>
          <Tr>
            <Th>{t('Protocol')}</Th>
            <Th>{t('Port Range')}</Th>
            <Th>{cidrLabel}</Th>
          </Tr>
        </Thead>
        <Tbody>
          {rules.map((rule, index) => (
            <Tr key={index}>
              <Td dataLabel={t('Protocol')}>{protocolToString(rule.protocol, t)}</Td>
              <Td dataLabel={t('Port Range')}>{formatPortRange(rule.portFrom, rule.portTo)}</Td>
              <Td dataLabel={cidrLabel}>{formatCidr(rule.ipv4Cidr, rule.ipv6Cidr)}</Td>
            </Tr>
          ))}
        </Tbody>
      </Table>
    </div>
  );
};
