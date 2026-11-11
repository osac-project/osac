import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { RoleBinding } from '@osac/types';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import RoleBindingActionsMenu from '../RoleBindingActionsMenu';
import RoleBindingStatusLabel from '../RoleBindingStatusLabel';
import { resolveRoleTitle } from '../utils';

const ROLE_BINDING_NAME_PREVIEW_LENGTH = 32;

interface RoleBindingsTableProps {
  roleBindings: RoleBinding[];
  rolesById: Map<string, string>;
}

const RoleBindingsTable = ({ roleBindings, rolesById }: RoleBindingsTableProps) => {
  const { t } = useTranslation();
  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      role: t('Role'),
      users: t('Users'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Role Bindings')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.role}</Th>
          <Th>{columnLabels.users}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {roleBindings.map((roleBinding) => (
          <Tr key={roleBinding.id}>
            <Td dataLabel={columnLabels.name} modifier="truncate">
              <ResourceNameField
                resource={roleBinding}
                maxCharsDisplayed={ROLE_BINDING_NAME_PREVIEW_LENGTH}
              />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <RoleBindingStatusLabel rb={roleBinding} />
            </Td>
            <Td dataLabel={columnLabels.role}>{resolveRoleTitle(roleBinding, rolesById)}</Td>
            <Td dataLabel={columnLabels.users}>
              {roleBinding.spec
                ? t('{{count}} user', { count: roleBinding.spec.users.length })
                : '—'}
            </Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={roleBinding.metadata?.creationTimestamp} />
            </Td>
            <Td isActionCell>
              <RoleBindingActionsMenu roleBinding={roleBinding} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default RoleBindingsTable;
