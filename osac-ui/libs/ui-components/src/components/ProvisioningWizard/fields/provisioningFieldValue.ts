import type { FieldPolicyLike } from '../../catalog/catalogFieldPolicyDisplay';
import { catalogFieldPolicyLocked } from '../../catalog/catalogFieldPolicyDisplay';

export interface ProvisioningFieldValue<T> {
  value: T;
  locked: boolean;
}

export interface ProvisioningFieldControlProps {
  name?: string;
  isDisabled?: boolean;
}

export const provisioningFieldValue = <T>(value: T, locked = false): ProvisioningFieldValue<T> => ({
  value,
  locked,
});

export const provisioningFieldFromPolicy = <T>(
  value: T,
  policy: FieldPolicyLike,
): ProvisioningFieldValue<T> => provisioningFieldValue(value, catalogFieldPolicyLocked(policy));
