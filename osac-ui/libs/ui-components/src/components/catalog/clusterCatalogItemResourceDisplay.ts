import type {
  BareMetalInstanceType,
  BareMetalInstanceTypeReference,
  ClusterCatalogItem,
  ClusterCatalogItemFields,
  ClusterCatalogNodeSet,
  ClusterNodeSetMapPolicy,
  ClusterVersion,
  ClusterVersionReference,
} from '@osac/types';

import {
  catalogFieldPolicyIsConfigured,
  catalogResourceHasDisplayValue,
} from './catalogFieldPolicyDisplay';

export const clusterCatalogItemFields = (
  catalogItem: ClusterCatalogItem,
): ClusterCatalogItemFields | undefined => catalogItem.fields;

export const clusterNodeSetMapPolicy = (
  fields: ClusterCatalogItemFields | undefined,
): ClusterNodeSetMapPolicy | undefined =>
  (fields as { nodeSets?: ClusterNodeSetMapPolicy } | undefined)?.nodeSets;

export const clusterVersionReference = (
  fields: ClusterCatalogItemFields | undefined,
): ClusterVersionReference | undefined => {
  if (fields?.version?.behavior.case === 'locked') {
    return fields.version.behavior.value;
  }
  if (fields?.version?.behavior?.case === 'editable') {
    return fields?.version?.behavior.value.defaultValue;
  }
  return undefined;
};

export const clusterNodeSetItemsFromPolicy = (
  policy: ClusterNodeSetMapPolicy | undefined,
): Record<string, ClusterCatalogNodeSet> | undefined => {
  if (policy?.behavior.case === 'locked') {
    return policy.behavior.value.items;
  }
  if (policy?.behavior.case === 'editable') {
    return policy.behavior.value.defaultValue?.items;
  }
  return undefined;
};

export const primaryClusterCatalogNodeSet = (
  items: Record<string, ClusterCatalogNodeSet> | undefined,
): { key: string; nodeSet: ClusterCatalogNodeSet } | undefined => {
  if (!items) {
    return undefined;
  }
  const keys = Object.keys(items).sort((a, b) => a.localeCompare(b));
  const key = keys[0];
  if (!key) {
    return undefined;
  }
  return { key, nodeSet: items[key] };
};

export const formatClusterCatalogNodeSetName = (nodeSetKey: string): string =>
  nodeSetKey.replace(/[-_]+/g, ' ').replace(/\b\w/g, (character) => character.toUpperCase());

export const findClusterVersionForReference = (
  versions: ClusterVersion[],
  reference: ClusterVersionReference | undefined,
): ClusterVersion | undefined => {
  if (!reference) {
    return undefined;
  }
  if (reference.id) {
    const byId = versions.find((version) => version.id === reference.id);
    if (byId) {
      return byId;
    }
  }
  if (reference.name) {
    return versions.find(
      (version) =>
        version.metadata?.name === reference.name &&
        version.metadata?.project === reference.project,
    );
  }
  return undefined;
};

export const formatClusterCatalogVersionLabel = (
  version: ClusterVersion | undefined,
  reference: ClusterVersionReference | undefined,
): string => {
  const raw =
    version?.spec?.version || version?.metadata?.name || reference?.name || reference?.id || '';
  if (!raw) {
    return '—';
  }
  if (/^openshift\s/i.test(raw)) {
    return raw;
  }
  return `OpenShift ${raw}`;
};

export const findBareMetalInstanceTypeForReference = (
  instanceTypes: BareMetalInstanceType[],
  reference: BareMetalInstanceTypeReference | undefined,
): BareMetalInstanceType | undefined => {
  if (!reference) {
    return undefined;
  }
  if (reference.id) {
    const byId = instanceTypes.find((instanceType) => instanceType.id === reference.id);
    if (byId) {
      return byId;
    }
  }
  if (reference.name) {
    return instanceTypes.find(
      (instanceType) =>
        instanceType.metadata?.name === reference.name &&
        instanceType.metadata?.project === reference.project,
    );
  }
  return undefined;
};

export const formatClusterCatalogBareMetalInstanceTypeLabel = (
  instanceType: BareMetalInstanceType | undefined,
  reference: BareMetalInstanceTypeReference | undefined,
): string => {
  if (instanceType?.metadata?.name) {
    return instanceType.metadata.name;
  }
  if (reference?.name) {
    return reference.name;
  }
  if (reference?.id) {
    return reference.id;
  }
  return '—';
};

export const formatClusterCatalogVersionRow = (
  fields: ClusterCatalogItemFields | undefined,
  version: ClusterVersion | undefined,
  reference: ClusterVersionReference | undefined,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.version)) {
    return undefined;
  }
  const label = formatClusterCatalogVersionLabel(version, reference);
  if (!catalogResourceHasDisplayValue(label)) {
    return undefined;
  }
  return label;
};

export const formatClusterCatalogNodeSetRow = (
  nodeSetsPolicy: ClusterNodeSetMapPolicy | undefined,
  primaryNodeSet: { key: string; nodeSet: ClusterCatalogNodeSet } | undefined,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(nodeSetsPolicy)) {
    return undefined;
  }
  if (!primaryNodeSet) {
    return undefined;
  }
  return formatClusterCatalogNodeSetName(primaryNodeSet.key);
};

export const formatClusterCatalogBareMetalInstanceTypeRow = (
  nodeSetsPolicy: ClusterNodeSetMapPolicy | undefined,
  primaryNodeSet: { key: string; nodeSet: ClusterCatalogNodeSet } | undefined,
  instanceType: BareMetalInstanceType | undefined,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(nodeSetsPolicy)) {
    return undefined;
  }
  if (!primaryNodeSet) {
    return undefined;
  }
  const label = formatClusterCatalogBareMetalInstanceTypeLabel(
    instanceType,
    primaryNodeSet.nodeSet.baremetalInstanceType,
  );
  if (!catalogResourceHasDisplayValue(label)) {
    return undefined;
  }
  return label;
};
