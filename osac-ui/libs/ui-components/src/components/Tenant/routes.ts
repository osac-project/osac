export const TENANTS_LIST_PATH = '/admin/tenants';

export const tenantEditPath = (tenantId: string): string =>
  `${TENANTS_LIST_PATH}/${encodeURIComponent(tenantId)}/edit`;

export const tenantBillingStepPath = (tenantId: string): string =>
  `${tenantEditPath(tenantId)}?step=billing`;
