import {
  type ProvisioningFieldValue,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

type PolicyBehaviorCase = 'locked' | 'editable';

/** Structural shape shared by every generated `*FieldPolicy` oneof. */
export type CatalogFieldPolicyLike =
  | {
      behavior?:
        | { case: 'locked'; value: unknown }
        | { case: 'editable'; value: unknown }
        | { case: undefined; value?: undefined };
    }
  | undefined;

/**
 * Decodes a generated `*FieldPolicy` message into the `{ value, locked }` shape the
 * catalog wizards share with provisioning. Absent policy and an editable policy with
 * no default both decode to `{ value: empty, locked: false }` so the UI cannot tell
 * them apart; the payload builder (`policyFromCatalogValue`) re-derives which one to
 * write based on whether the field is empty at submit time.
 */
export const catalogValueFromPolicy = <T>(
  policy: CatalogFieldPolicyLike,
  readValue: (policyValue: unknown, behavior: PolicyBehaviorCase) => T,
  empty: T,
): ProvisioningFieldValue<T> => {
  const behavior = policy?.behavior;
  if (behavior?.case === 'locked') {
    return provisioningFieldValue(readValue(behavior.value, 'locked'), true);
  }
  if (behavior?.case === 'editable') {
    const defaultValue = (behavior.value as { defaultValue?: unknown })?.defaultValue;
    if (defaultValue === undefined) {
      return provisioningFieldValue(empty, false);
    }
    return provisioningFieldValue(readValue(defaultValue, 'editable'), false);
  }
  return provisioningFieldValue(empty, false);
};

/**
 * Encodes a `{ value, locked }` field back into a generated `*FieldPolicy` message.
 * `locked: true` always writes the locked arm, even for an empty value, so step
 * validation (not this function) is responsible for rejecting an empty locked field.
 * `locked: false` omits the policy entirely when the value is empty, which is how an
 * editable field with no default is distinguished from one with a default.
 */
export const policyFromCatalogValue = <TPolicy>(
  field: ProvisioningFieldValue<unknown>,
  isEmpty: (value: unknown) => boolean,
  toLocked: (value: unknown) => TPolicy,
  toEditable: (value: unknown) => TPolicy,
): TPolicy | undefined => {
  if (field.locked) {
    return toLocked(field.value);
  }
  if (isEmpty(field.value)) {
    return undefined;
  }
  return toEditable(field.value);
};
