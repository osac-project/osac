import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

/** Structural shape shared by every generated `*FieldPolicy` oneof. */
export type CatalogFieldPolicyLike<TPolicyValue> =
  | {
      behavior?:
        | { case: 'locked'; value: TPolicyValue }
        | { case: 'editable'; value: { defaultValue?: TPolicyValue } }
        | { case: undefined; value?: undefined };
    }
  | undefined;

/**
 * Encodes a `{ value, locked }` field back into a generated `*FieldPolicy` message.
 * `locked: true` always writes the locked arm, even for an empty value, so step
 * validation (not this function) is responsible for rejecting an empty locked field.
 * `locked: false` omits the policy entirely when the value is empty, which is how an
 * editable field with no default is distinguished from one with a default.
 */
export const policyFromCatalogValue = <TValue, TPolicy>(
  field: ProvisioningFieldValue<TValue>,
  isEmpty: (value: TValue) => boolean,
  toLocked: (value: TValue) => TPolicy,
  toEditable: (value: TValue) => TPolicy,
): TPolicy | undefined => {
  if (field.locked) {
    return toLocked(field.value);
  }
  if (isEmpty(field.value)) {
    return undefined;
  }
  return toEditable(field.value);
};

/** Encodes the common locked/editable shape used by catalog field policies. */
export const catalogFieldPolicyFromValue = <TValue, TPolicyValue, TPolicy>(
  field: ProvisioningFieldValue<TValue>,
  isEmpty: (value: TValue) => boolean,
  toValue: (value: TValue) => TPolicyValue,
): TPolicy | undefined =>
  policyFromCatalogValue<TValue, TPolicy>(
    field,
    isEmpty,
    (value) => ({ behavior: { case: 'locked', value: toValue(value) } }) as TPolicy,
    (value) =>
      ({ behavior: { case: 'editable', value: { defaultValue: toValue(value) } } }) as TPolicy,
  );

/** Returns the update-mask path needed to clear an existing catalog policy. */
export const catalogFieldPolicyClearPath = <TPolicyValue, TValue>(
  policy: CatalogFieldPolicyLike<TPolicyValue>,
  value: TValue | undefined,
  path: string,
): string[] => (policy?.behavior?.case && value === undefined ? [path] : []);
