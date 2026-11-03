import {
  BareMetalInstanceConditionType,
  ClusterConditionType,
  ComputeInstanceConditionType,
  ConditionStatus,
} from '@osac/types';

const BM_CONDITION_TYPE_PREFIX = /^BARE_METAL_INSTANCE_CONDITION_TYPE_/;
const VM_CONDITION_TYPE_PREFIX = /^COMPUTE_INSTANCE_CONDITION_TYPE_/;

export type ConditionResourceKind = 'cluster' | 'compute_instance' | 'bare_metal_instance';

export const humanizeConditionType = (
  type: ClusterConditionType | ComputeInstanceConditionType | BareMetalInstanceConditionType,
  resourceKind: ConditionResourceKind,
): string => {
  if (resourceKind === 'cluster') {
    const clusterName = ClusterConditionType[type as ClusterConditionType];
    if (typeof clusterName === 'string') {
      return clusterName.replace(/_/g, ' ');
    }
  } else if (resourceKind === 'compute_instance') {
    const vmName = ComputeInstanceConditionType[type as ComputeInstanceConditionType];
    if (typeof vmName === 'string') {
      return vmName.replace(VM_CONDITION_TYPE_PREFIX, '').replace(/_/g, ' ');
    }
  } else {
    const bmName = BareMetalInstanceConditionType[type as BareMetalInstanceConditionType];
    if (typeof bmName === 'string') {
      return bmName.replace(BM_CONDITION_TYPE_PREFIX, '').replace(/_/g, ' ');
    }
  }
  return String(type);
};

export const formatConditionStatusForDisplay = (status: ConditionStatus): string => {
  switch (status) {
    case ConditionStatus.TRUE:
      return 'True';
    case ConditionStatus.FALSE:
      return 'False';
    default:
      return 'Unknown';
  }
};

export const formatIsoDate = (iso?: string): string => {
  if (!iso?.trim()) {
    return '—';
  }
  const t = Date.parse(iso.trim());
  return Number.isNaN(t) ? iso : new Date(t).toLocaleString();
};

export const displayValue = (value?: string): string => value?.trim() || '—';

export const formatSizeGib = (sizeGib: bigint | undefined): string =>
  sizeGib === undefined ? '—' : `${sizeGib} GiB`;
