import type { FieldPolicyLike } from '../../catalog/catalogFieldPolicyDisplay';

export interface ProvisioningFieldValue<T> {
  value: T;
  locked: boolean;
}

export interface ProvisioningFieldControlProps {
  name: string;
  isDisabled?: boolean;
}

export const provisioningFieldValue = <T>(value: T, locked = false): ProvisioningFieldValue<T> => ({
  value,
  locked,
});

/** Extracts a catalog policy's backend value without deserializing it for a form. */
export const getFieldFromCatalogItemPolicy = <V>(
  policy: FieldPolicyLike<V>,
  defaultValue: V,
): ProvisioningFieldValue<V> => {
  if (policy?.behavior?.case === 'locked') {
    return provisioningFieldValue((policy.behavior.value as V | undefined) ?? defaultValue, true);
  }

  if (policy?.behavior?.case === 'editable') {
    const editableValue = policy.behavior.value as { defaultValue?: V } | undefined;
    return provisioningFieldValue(editableValue?.defaultValue ?? defaultValue, false);
  }

  return provisioningFieldValue(defaultValue, false);
};
