import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { RoleBindings, Roles } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import RoleBindingActionsMenu from './RoleBindingActionsMenu';
import RoleBindingStatusLabel from './RoleBindingStatusLabel';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { SubtleContent } from '../../components/SubtleContent/SubtleContent';
import { useTranslation } from '../../hooks/useTranslation';

const RoleBindingsPage = () => {
  const { t } = useTranslation();

  const { data: roleBindings, isLoading, error } = useListResource(RoleBindings);
  const { data: roles } = useListResource(Roles);

  const rolesById = useMemo(() => {
    const map = new Map<string, string>();
    if (roles?.items) {
      for (const role of roles.items) {
        map.set(role.id, role.spec?.title || role.metadata?.name || role.id);
      }
    }
    return map;
  }, [roles?.items]);

  return (
    <ListPage
      title={t('Role Bindings')}
      description={t('Manage role bindings for users.')}
      error={error}
      actions={
        <CreateButton service={RoleBindings} to="create">
          {t('Create role binding')}
        </CreateButton>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        {!roleBindings?.items.length ? (
          <SubtleContent component="p">{t('No role bindings available.')}</SubtleContent>
        ) : (
          <Table aria-label={t('Role Bindings')} variant="compact">
            <Thead>
              <Tr>
                <Th>{t('Name')}</Th>
                <Th>{t('Status')}</Th>
                <Th>{t('Role')}</Th>
                <Th>{t('Users')}</Th>
                <Th aria-label={t('Actions')} />
              </Tr>
            </Thead>
            <Tbody>
              {roleBindings.items.map((rb) => (
                <Tr key={rb.id}>
                  <Td dataLabel={t('Name')}>
                    <ResourceNameField resource={rb} />
                  </Td>
                  <Td dataLabel={t('Status')}>
                    <RoleBindingStatusLabel rb={rb} />
                  </Td>
                  <Td dataLabel={t('Role')}>
                    {rb.spec?.role?.name ? rolesById.get(rb.spec.role.id) : '-'}
                  </Td>
                  <Td dataLabel={t('Users')}>
                    {rb.spec ? t('{{count}} user', { count: rb.spec.users.length }) : '-'}
                  </Td>
                  <Td isActionCell>
                    <RoleBindingActionsMenu roleBinding={rb} />
                  </Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        )}
      </ListPageBody>
    </ListPage>
  );
};

export default RoleBindingsPage;
