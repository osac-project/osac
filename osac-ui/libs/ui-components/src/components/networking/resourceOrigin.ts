import type { TFunction } from 'i18next';

export const DEFAULT_RESOURCE_LABEL = 'osac.openshift.io/default';
export const AUTO_CREATED_LABEL = 'osac.openshift.io/auto-created';
export const AUTO_CREATED_FOR_LABEL = 'osac.openshift.io/auto-created-for';
export const AUTO_CREATED_KIND_LABEL = 'osac.openshift.io/auto-created-kind';

export interface LabeledResource {
  metadata?: { labels?: Record<string, string> };
  /** An ExternalIPAttachment has no kind label; its `spec.target` names the owner. */
  spec?: unknown;
}

interface TargetReference {
  case?: string;
  value?: { id?: string; name?: string };
}

export const isDefaultResource = (resource?: LabeledResource): boolean =>
  resource?.metadata?.labels?.[DEFAULT_RESOURCE_LABEL] === 'true';

export const isAutoCreatedResource = (resource?: LabeledResource): boolean =>
  resource?.metadata?.labels?.[AUTO_CREATED_LABEL] === 'true';

/** The lowercase kind of a workload, from an auto-created-kind label or an API oneof case. */
export const workloadKindLabel = (t: TFunction, kind?: string): string | undefined => {
  switch (kind) {
    case 'cluster':
      return t('cluster');
    case 'compute_instance':
    case 'computeInstance':
      return t('virtual machine');
    case 'bare_metal_instance':
    case 'baremetalInstance':
      return t('bare-metal instance');
    default:
      return undefined;
  }
};

interface AutoCreatedOwner {
  kind: string;
  name: string;
}

// The kind label and the auto-created-for label (the owner's API ID) name the owner. An
// ExternalIPAttachment has no kind label, so its target (kind and name) names it.
const autoCreatedOwner = (
  t: TFunction,
  resource?: LabeledResource,
): AutoCreatedOwner | undefined => {
  const labels = resource?.metadata?.labels;
  const target = (resource?.spec as { target?: TargetReference } | undefined)?.target;
  const kind = workloadKindLabel(t, labels?.[AUTO_CREATED_KIND_LABEL] ?? target?.case);
  const name = labels?.[AUTO_CREATED_KIND_LABEL]
    ? labels[AUTO_CREATED_FOR_LABEL]
    : target?.value?.name || target?.value?.id || labels?.[AUTO_CREATED_FOR_LABEL];
  return kind && name ? { kind, name } : undefined;
};

export const autoCreatedTooltip = (t: TFunction, resource?: LabeledResource): string => {
  const owner = autoCreatedOwner(t, resource);
  return owner
    ? t('Created automatically for {{kind}} {{name}}. It is deleted with it.', { ...owner })
    : t('Created automatically by OSAC. It is deleted with its owner.');
};

export const defaultResourceTooltip = (t: TFunction): string =>
  t('Created automatically as a tenant default.');

/**
 * Default and automatic resources cannot be deleted, detached, or (automatic
 * SecurityGroups) edited by a tenant; the UI shows those actions disabled with
 * `protectedResourceReason` as the reason.
 */
export const isProtectedResource = (resource?: LabeledResource): boolean =>
  isDefaultResource(resource) || isAutoCreatedResource(resource);

/** Why a Default or Auto resource cannot be deleted or changed, or undefined. */
export const protectedResourceReason = (
  t: TFunction,
  resource?: LabeledResource,
): string | undefined => {
  if (isDefaultResource(resource)) {
    return t('Created as a tenant default. It is deleted with the tenant.');
  }
  return isAutoCreatedResource(resource) ? autoCreatedTooltip(t, resource) : undefined;
};
