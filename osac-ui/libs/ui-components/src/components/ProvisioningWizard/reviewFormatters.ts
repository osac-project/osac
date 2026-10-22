export const formatReviewScalar = (value: unknown, sensitive = false): string => {
  if (value === undefined || value === null || value === '') {
    return '—';
  }
  if (sensitive) {
    return '••••••••';
  }
  return typeof value === 'object'
    ? (JSON.stringify(value) ?? '—')
    : String(value as string | number | boolean);
};

export const formatBootDiskSizeForReview = (value: unknown): string => {
  if (value === undefined || value === null || value === '') {
    return '—';
  }
  return `${typeof value === 'object' ? (JSON.stringify(value) ?? '—') : String(value as string | number | boolean)} GB`;
};
