import type { TFunction } from 'i18next';

/** Label the system sets on the tenant's default resources. */
export const DEFAULT_LABEL = 'osac.openshift.io/default';

/** Label the system sets on resources created on behalf of another resource. */
export const AUTO_CREATED_LABEL = 'osac.openshift.io/auto-created-for';

export type ResourceOrigin = 'default' | 'auto';

type Labels = Record<string, string> | undefined;

/**
 * How the system owns a resource: the tenant default network, or a resource
 * created automatically for another resource. `undefined` for everything a
 * user created.
 */
export const resourceOrigin = (labels: Labels): ResourceOrigin | undefined => {
  if (labels?.[DEFAULT_LABEL] === 'true') {
    return 'default';
  }
  if (labels?.[AUTO_CREATED_LABEL]) {
    return 'auto';
  }
  return undefined;
};

/** The resource that created an automatic resource, from its label value. */
export const autoCreatedFor = (labels: Labels): string | undefined =>
  labels?.[AUTO_CREATED_LABEL] || undefined;

/** System-owned resources cannot be deleted on their own. */
export const isProtectedResource = (labels: Labels): boolean =>
  resourceOrigin(labels) !== undefined;

/**
 * Why Delete is disabled for a system-owned resource. Names the owner of an
 * automatic resource, and says how a default resource is removed.
 */
export const protectedResourceReason = (t: TFunction, labels: Labels): string | undefined => {
  switch (resourceOrigin(labels)) {
    case 'default':
      return t('This is a default resource. It is removed with its tenant.');
    case 'auto': {
      const owner = autoCreatedFor(labels);
      return owner
        ? t('This resource was created automatically by {{owner}}.', { owner })
        : t('This resource was created automatically and is removed with what created it.');
    }
    default:
      return undefined;
  }
};
