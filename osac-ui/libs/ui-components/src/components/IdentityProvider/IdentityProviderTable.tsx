import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { IdentityProvider } from '@osac/types';

import IdentityProviderActionsMenu from './IdentityProviderActionsMenu';
import IdentityProviderStatusLabel from './IdentityProviderStatusLabel';
import { getIdpIssuerUrl, getIdpName, resolveIdpType } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import TruncatedText from '../Primitives/TruncatedText';
import ResourceNameField from '../Resource/ResourceNameField';

interface IdentityProviderTableProps {
  identityProviders: IdentityProvider[];
}

const IdentityProviderTable = ({ identityProviders }: IdentityProviderTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      enabled: t('Enabled'),
      status: t('Status'),
      protocol: t('Protocol'),
      issuerUrl: t('Issuer URL'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Identity providers')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.enabled}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.protocol}</Th>
          <Th>{columnLabels.issuerUrl}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {identityProviders.map((idp) => {
          const issuerUrl = getIdpIssuerUrl(idp);

          return (
            <Tr key={idp.id}>
              <Td dataLabel={columnLabels.name}>
                <ResourceNameField
                  resource={idp}
                  title={getIdpName(idp)}
                  subTitle={idp.metadata?.tenant}
                />
              </Td>
              <Td dataLabel={columnLabels.enabled}>
                {idp.spec?.enabled ? t('Enabled') : t('Disabled')}
              </Td>
              <Td dataLabel={columnLabels.status}>
                <IdentityProviderStatusLabel idp={idp} />
              </Td>
              <Td dataLabel={columnLabels.protocol}>{resolveIdpType(t, idp.spec?.config.case)}</Td>
              <Td dataLabel={columnLabels.issuerUrl}>
                {issuerUrl ? <TruncatedText content={issuerUrl} maxCharsDisplayed={48} /> : '-'}
              </Td>
              <Td dataLabel={columnLabels.created}>
                <Timestamp value={idp.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={columnLabels.actions} isActionCell>
                <IdentityProviderActionsMenu idp={idp} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default IdentityProviderTable;
