import type { ReactNode } from 'react';

import type { FieldPolicyLike } from '../../catalog/catalogFieldPolicyDisplay';

export interface ProvisioningFieldValue<T> {
  value: T;
  locked: boolean;
}

export interface ProvisioningFieldControlProps {
  name: string;
  isDisabled?: boolean;
  /** Right side of the control's label line. Catalog authoring passes the Editable/Locked switch here. */
  labelInfo?: ReactNode;
  /** Catalog authoring sets this from Locked vs Editable. Provisioning callers keep today's required default. */
  isRequired?: boolean;
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

export interface UserDataSourceFormFields {
  userDataSource: ProvisioningFieldValue<'inline' | 'secret'>;
  userData: ProvisioningFieldValue<string>;
  userDataSecret: ProvisioningFieldValue<string>;
}

interface CatalogUserDataSource {
  source?: {
    case?: string;
    value?: unknown;
  };
}

/** Maps the complete OSAC-7033 user data source policy into wizard form fields. */
export const getUserDataSourceFieldsFromPolicy = (
  policy: FieldPolicyLike<unknown>,
): UserDataSourceFormFields => {
  const field = getFieldFromCatalogItemPolicy(policy, undefined);
  const source = (field.value as CatalogUserDataSource | undefined)?.source;
  const isSecret = source?.case === 'secret';
  const inlineValue = !isSecret && source?.case === 'inline' ? source.value : undefined;
  const secretValue =
    isSecret && typeof source.value === 'object' && source.value !== null
      ? (source.value as { name?: unknown }).name
      : undefined;

  return {
    userDataSource: provisioningFieldValue(isSecret ? 'secret' : 'inline', field.locked),
    userData: provisioningFieldValue(
      typeof inlineValue === 'string' ? inlineValue : '',
      field.locked,
    ),
    userDataSecret: provisioningFieldValue(
      typeof secretValue === 'string' ? secretValue : '',
      field.locked,
    ),
  };
};
