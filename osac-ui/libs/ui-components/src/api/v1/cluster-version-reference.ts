import { type ClusterVersion, type ClusterVersionReference } from '@osac/types';

export const matchesClusterVersionReference = (
  reference: ClusterVersionReference,
  version: ClusterVersion,
): boolean => {
  const metadata = version.metadata;
  if (reference.id) {
    // Empty fields on an ID reference are protobuf defaults, not scope constraints.
    return (
      version.id === reference.id &&
      (reference.name === '' || metadata?.name === reference.name) &&
      (reference.project === '' || metadata?.project === reference.project) &&
      (reference.shared === false || metadata?.tenant === 'shared')
    );
  }

  // A name needs exact scope because different projects can use the same name.
  return (
    reference.name !== '' &&
    metadata?.name === reference.name &&
    metadata?.project === reference.project &&
    (metadata?.tenant === 'shared') === reference.shared
  );
};
