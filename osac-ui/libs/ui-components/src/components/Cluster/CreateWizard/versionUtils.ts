import { ClusterVersion, ClusterVersionState } from '@osac/types';

export const isDeprecatedVersion = (version: ClusterVersion | undefined): boolean =>
  version?.spec?.state === ClusterVersionState.DEPRECATED;

/** Human-readable version (spec.version), falling back to metadata.name then the given name. */
export const versionDisplayName = (
  version: ClusterVersion | undefined,
  fallbackName = '',
): string => version?.spec?.version || version?.metadata?.name || fallbackName;

/** Find a cluster version by its metadata.name (the value stored in the form). */
export const findVersionByName = (
  versions: ClusterVersion[],
  name: string,
): ClusterVersion | undefined => versions.find((version) => version.metadata?.name === name);
