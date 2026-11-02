import type { ReactNode } from 'react';
import type { MessageInitShape } from '@bufbuild/protobuf';

import { type UserDataSource, UserDataSourceSchema } from '@osac/types';

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

export type UserDataSourceKind = 'inline' | 'secret';

export interface UserDataSourceFormValue {
  source: UserDataSourceKind;
  inline: string;
  secret: string;
}

export type UserDataSourceFormField = ProvisioningFieldValue<UserDataSourceFormValue>;

/** Maps the complete OSAC-7033 user data source policy into wizard form fields. */
export const getUserDataSourceFieldsFromPolicy = (
  policy: FieldPolicyLike<unknown>,
): UserDataSourceFormField => {
  const field = getFieldFromCatalogItemPolicy(policy, undefined);
  const source = (field.value as UserDataSource | undefined)?.source;
  const isSecret = source?.case === 'secret';
  const inlineValue = source?.case === 'inline' ? source.value : undefined;
  const secretValue = source?.case === 'secret' ? source.value.name : undefined;

  return provisioningFieldValue(
    {
      source: isSecret ? 'secret' : 'inline',
      inline: typeof inlineValue === 'string' ? inlineValue : '',
      secret: typeof secretValue === 'string' ? secretValue : '',
    },
    field.locked,
  );
};

/** Serializes the selected user-data source unless it is locked or empty. */
export const getUserDataSourcePayload = (
  fields: UserDataSourceFormField,
  initialFields?: UserDataSourceFormField,
): MessageInitShape<typeof UserDataSourceSchema> | undefined => {
  if (fields.locked) {
    return undefined;
  }

  const { source, inline, secret } = fields.value;
  const value = (source === 'secret' ? secret : inline).trim();
  if (!value) {
    const initialValue = initialFields
      ? (initialFields.value[initialFields.value.source] ?? '').trim()
      : '';
    return initialValue ? { source: { case: 'inline', value: '' } } : undefined;
  }

  return source === 'secret'
    ? { source: { case: 'secret', value: { name: value } } }
    : { source: { case: 'inline', value } };
};
