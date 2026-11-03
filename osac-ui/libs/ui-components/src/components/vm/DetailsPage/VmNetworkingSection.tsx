import { Stack, StackItem, Title } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ComputeInstance } from '@osac/types';

import { useVmNetworkAttachmentRows } from './useVmNetworkAttachmentRows';
import { useTranslation } from '../../../hooks/useTranslation';
import ResourceLink from '../../Resource/ResourceLink';
import { SubtleContent } from '../../SubtleContent/SubtleContent';

import './VmNetworkingSection.css';

interface VmNetworkingSectionProps {
  vm: ComputeInstance;
}

const VmNetworkingSection = ({ vm }: VmNetworkingSectionProps) => {
  const { t } = useTranslation();
  const rows = useVmNetworkAttachmentRows(vm);

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Networking')}
        </Title>
      </StackItem>
      <StackItem>
        {rows.length > 0 ? (
          <Table
            aria-label={t('Networking')}
            variant="compact"
            borders
            className="osac-vm-networking-table"
          >
            <Thead>
              <Tr>
                <Th>{t('Virtual network')}</Th>
                <Th>{t('Subnet')}</Th>
                <Th>{t('Security groups')}</Th>
                <Th>{t('Internal IP')}</Th>
              </Tr>
            </Thead>
            <Tbody>
              {rows.map((row, index) => (
                <Tr key={`network-attachment-${index}`}>
                  <Td dataLabel={t('Virtual network')}>
                    {row.virtualNetwork ? (
                      <ResourceLink
                        basePath="/networking/virtual-networks"
                        id={row.virtualNetwork.id}
                        name={row.virtualNetwork.name}
                      />
                    ) : (
                      '-'
                    )}
                  </Td>
                  <Td dataLabel={t('Subnet')}>{row.subnet}</Td>
                  <Td dataLabel={t('Security groups')}>
                    {row.securityGroups.length > 0
                      ? row.securityGroups.map((securityGroup, securityGroupIndex) => (
                          <span key={securityGroup.id}>
                            {securityGroupIndex > 0 ? ', ' : null}
                            <ResourceLink
                              basePath="/networking/security-groups"
                              id={securityGroup.id}
                              name={securityGroup.name}
                            />
                          </span>
                        ))
                      : '-'}
                  </Td>
                  <Td dataLabel={t('Internal IP')}>{row.internalIp}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        ) : (
          <SubtleContent component="p">{t('No virtual networks configured.')}</SubtleContent>
        )}
      </StackItem>
    </Stack>
  );
};

export default VmNetworkingSection;
