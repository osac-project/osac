import { type CSSProperties, useId } from 'react';
import { Button, Tooltip } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import type { TFunction } from 'i18next';

import { Protocol, type SecurityRule } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import { useTranslation } from '../../hooks/useTranslation';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface SecurityGroupRulesTableProps {
  rules: SecurityRule[];
  direction: 'ingress' | 'egress';
  editingDisabledReason?: string;
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

const formatCidr = (ipv4Cidr?: string): string => ipv4Cidr || '—';

export const SecurityGroupRulesTable = ({
  rules,
  direction,
  editingDisabledReason,
  onAddRule,
  onEditRule,
  onDeleteRule,
}: SecurityGroupRulesTableProps) => {
  const { t } = useTranslation();
  const reasonId = useId();
  const disabledReason = editingDisabledReason?.trim() || undefined;
  const editingDisabled = disabledReason !== undefined;

  const cidrLabel = direction === 'ingress' ? t('Source CIDR') : t('Destination CIDR');

  const addButton = (style: CSSProperties) => (
    <Button
      variant="primary"
      isAriaDisabled={editingDisabled}
      aria-describedby={editingDisabled ? reasonId : undefined}
      onClick={onAddRule}
      style={style}
    >
      {t('Add rule')}
    </Button>
  );

  const addAction = (style: CSSProperties) =>
    disabledReason ? (
      <Tooltip content={disabledReason}>{addButton(style)}</Tooltip>
    ) : (
      addButton(style)
    );

  const reason = disabledReason ? (
    <SubtleContent component="p" id={reasonId}>
      {disabledReason}
    </SubtleContent>
  ) : null;

  if (rules.length === 0) {
    return (
      <div>
        {reason}
        <SubtleContent component="p">
          {editingDisabled
            ? direction === 'ingress'
              ? t('No inbound rules')
              : t('No outbound rules')
            : direction === 'ingress'
              ? t('No inbound rules yet. Add one to allow incoming traffic.')
              : t('No outbound rules yet. Add one to allow outgoing traffic.')}
        </SubtleContent>
        {!editingDisabled && addAction({ marginTop: '1rem' })}
      </div>
    );
  }

  return (
    <div>
      {reason}
      {addAction({ marginBottom: '1rem' })}
      <Table
        aria-label={`${direction} rules`}
        aria-describedby={editingDisabled ? reasonId : undefined}
        variant="compact"
        borders
      >
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
              <Td dataLabel={cidrLabel}>{formatCidr(rule.ipv4Cidr)}</Td>
              <Td dataLabel={t('Actions')}>
                {disabledReason ? (
                  <Tooltip content={disabledReason}>
                    <Button
                      variant="link"
                      isInline
                      isAriaDisabled
                      aria-describedby={reasonId}
                      onClick={() => onEditRule(index)}
                    >
                      {t('Edit')}
                    </Button>
                  </Tooltip>
                ) : (
                  <Button variant="link" isInline onClick={() => onEditRule(index)}>
                    {t('Edit')}
                  </Button>
                )}
                {' | '}
                <DeleteResourceButton
                  variant="link"
                  isInline
                  canDelete={!editingDisabled}
                  disabledReason={disabledReason}
                  onClick={() => onDeleteRule(index)}
                />
              </Td>
            </Tr>
          ))}
        </Tbody>
      </Table>
    </div>
  );
};
