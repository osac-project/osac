export const formatClusterNodeSetReview = (hostType: string, size: number | undefined): string =>
  `${hostType}: ${size || 0}`;
