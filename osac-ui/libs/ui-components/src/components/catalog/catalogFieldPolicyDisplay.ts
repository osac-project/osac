export type CatalogFieldPolicyBehavior = 'locked' | 'editable';

export type FieldPolicyLike<T = unknown> =
  | {
      behavior?: { case?: string; value?: T | { defaultValue?: T } };
    }
  | undefined;

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

export const catalogFieldPolicyLocked = (policy: FieldPolicyLike): boolean =>
  catalogFieldPolicyBehavior(policy) === 'locked';

export const catalogFieldPolicyValue = <T>(policy: FieldPolicyLike<T>): T | undefined => {
  const behavior = policy?.behavior;
  if (behavior?.case === 'locked') {
    return behavior.value as T;
  }
  if (behavior?.case === 'editable' && behavior.value && typeof behavior.value === 'object') {
    return (behavior.value as { defaultValue?: T }).defaultValue;
  }
  return undefined;
};

export const CATALOG_RESOURCE_EMPTY_DISPLAY = '—';

export const catalogResourceHasDisplayValue = (display: string): boolean =>
  display !== CATALOG_RESOURCE_EMPTY_DISPLAY;
