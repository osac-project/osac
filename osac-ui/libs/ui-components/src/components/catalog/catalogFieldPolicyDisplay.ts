export type CatalogFieldPolicyBehavior = 'locked' | 'editable';

type FieldPolicyLike = { behavior?: { case?: string } } | undefined;

export const catalogFieldPolicyBehavior = (
  policy: FieldPolicyLike,
): CatalogFieldPolicyBehavior | undefined => {
  const behaviorCase = policy?.behavior?.case;
  if (behaviorCase === 'locked' || behaviorCase === 'editable') {
    return behaviorCase;
  }
  return undefined;
};

export const catalogFieldPolicyIsConfigured = (policy: FieldPolicyLike): boolean =>
  catalogFieldPolicyBehavior(policy) !== undefined;

export const CATALOG_RESOURCE_EMPTY_DISPLAY = '—';

export const catalogResourceHasDisplayValue = (display: string): boolean =>
  display !== CATALOG_RESOURCE_EMPTY_DISPLAY;
