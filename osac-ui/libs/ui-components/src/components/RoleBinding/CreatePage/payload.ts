import { MessageInitShape } from '@bufbuild/protobuf';

import { RoleBindingSpecSchema } from '@osac/types';

import { RoleBindingCreateFormValues } from './values';

export const getRoleBindingSpec = (
  values: RoleBindingCreateFormValues,
): MessageInitShape<typeof RoleBindingSpecSchema> => ({
  role: {
    name: values.role,
    tenant: 'shared',
  },
  users: values.users.map((u) => ({ name: u })),
});
