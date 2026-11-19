import type { RoleBinding } from '@osac/types';

export const resolveRoleTitle = (
  roleBinding: RoleBinding,
  rolesById: Map<string, string>,
): string => {
  const roleId = roleBinding.spec?.role?.id;
  if (!roleId) {
    return '—';
  }
  return rolesById.get(roleId) ?? roleBinding.spec?.role?.name ?? roleId;
};
