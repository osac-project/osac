export const DEFAULT_NETWORKING_RESOURCE_LABEL = 'osac.openshift.io/default';
export const AUTO_NETWORKING_RESOURCE_LABEL = 'osac.openshift.io/auto-created-for';

/** Why OSAC, rather than the user, owns a networking resource. */
export type NetworkingResourceOrigin = 'default' | 'auto';

interface LabelledResource {
  metadata?: {
    labels?: Record<string, string>;
  };
}

/**
 * The tenant default is marked by the `osac.openshift.io/default` label; an Auto
 * resource names the resource it was created for in `osac.openshift.io/auto-created-for`.
 * A resource that carries both is reported as the default, because users cannot
 * delete it for that reason regardless of what created it.
 */
export const networkingResourceOrigin = (
  resource?: LabelledResource,
): NetworkingResourceOrigin | undefined => {
  const labels = resource?.metadata?.labels;
  if (labels?.[DEFAULT_NETWORKING_RESOURCE_LABEL] === 'true') {
    return 'default';
  }
  if (labels?.[AUTO_NETWORKING_RESOURCE_LABEL]) {
    return 'auto';
  }
  return undefined;
};
