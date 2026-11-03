import type { ProvisioningFieldValue } from '../ProvisioningWizard/fields/provisioningFieldValue';

/** Structural shape shared by every generated `*FieldPolicy` oneof. */
export type CatalogFieldPolicyLike<TPolicyValue> =
  | {
      behavior?:
        | { case: 'locked'; value: TPolicyValue }
        | { case: 'editable'; value: { defaultValue?: TPolicyValue } }
        | { case: undefined; value?: undefined };
    }
  | undefined;

/** Encodes a catalog field's value in its locked or editable policy arm. */
export const catalogFieldPolicyFromValue = <TValue, TPolicyValue = TValue>(
  field: ProvisioningFieldValue<TValue>,
  isEmpty: (value: TValue) => boolean,
  toValue: (value: TValue) => TPolicyValue = (value) => value as unknown as TPolicyValue,
): CatalogFieldPolicyLike<TPolicyValue> => {
  if (field.locked) {
    return { behavior: { case: 'locked', value: toValue(field.value) } };
  }
  if (isEmpty(field.value)) {
    return undefined;
  }
  return { behavior: { case: 'editable', value: { defaultValue: toValue(field.value) } } };
};

/** Returns the update-mask path needed to clear an existing catalog policy. */
export const catalogFieldPolicyClearPath = <TPolicyValue, TValue>(
  policy: CatalogFieldPolicyLike<TPolicyValue>,
  value: TValue | undefined,
  path: string,
): string[] => (policy?.behavior?.case && value === undefined ? [path] : []);
