import { Button } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import type { TFunction } from 'i18next';

import { Protocol, type SecurityRule } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import { useTranslation } from '../../hooks/useTranslation';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface SecurityGroupRulesTableProps {
  rules: SecurityRule[];
  direction: 'ingress' | 'egress';
  onAddRule: () => void;
  onEditRule: (index: number) => void;
  onDeleteRule: (index: number) => void;
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

export const SecurityGroupRulesTable = ({
  rules,
  direction,
  onAddRule,
  onEditRule,
  onDeleteRule,
}: SecurityGroupRulesTableProps) => {
  const { t } = useTranslation();

  const cidrLabel = direction === 'ingress' ? t('Source CIDR') : t('Destination CIDR');

  if (rules.length === 0) {
    return (
      <div>
        <SubtleContent component="p">
          {direction === 'ingress'
            ? t('No inbound rules yet. Add one to allow incoming traffic.')
            : t('No outbound rules yet. Add one to allow outgoing traffic.')}
        </SubtleContent>
        <Button variant="primary" onClick={onAddRule} style={{ marginTop: '1rem' }}>
          {t('Add rule')}
        </Button>
      </div>
    );
  }

  return (
    <div>
      <Button variant="primary" onClick={onAddRule} style={{ marginBottom: '1rem' }}>
        {t('Add rule')}
      </Button>
      <Table aria-label={`${direction} rules`} variant="compact" borders>
        <Thead>
          <Tr>
            <Th>{t('Protocol')}</Th>
            <Th>{t('Port Range')}</Th>
            <Th>{cidrLabel}</Th>
            <Th>{t('Actions')}</Th>
          </Tr>
        </Thead>
        <Tbody>
          {rules.map((rule, index) => (
            <Tr key={index}>
              <Td dataLabel={t('Protocol')}>{protocolToString(rule.protocol, t)}</Td>
              <Td dataLabel={t('Port Range')}>{formatPortRange(rule.portFrom, rule.portTo)}</Td>
              <Td dataLabel={cidrLabel}>{formatCidr(rule.ipv4Cidr, rule.ipv6Cidr)}</Td>
              <Td dataLabel={t('Actions')}>
                <Button variant="link" isInline onClick={() => onEditRule(index)}>
                  {t('Edit')}
                </Button>
                {' | '}
                <DeleteResourceButton variant="link" isInline onClick={() => onDeleteRule(index)} />
              </Td>
            </Tr>
          ))}
        </Tbody>
      </Table>
    </div>
  );
};
