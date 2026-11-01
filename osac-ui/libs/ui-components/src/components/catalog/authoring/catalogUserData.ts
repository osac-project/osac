import type { UserDataSource } from '@osac/types';

import { type CatalogFieldPolicyLike, catalogValueFromPolicy } from './catalogItemPolicy';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface CatalogUserDataValue {
  source: 'inline' | 'secret';
  value: string;
}

export interface CatalogUserDataFormValues {
  userData: ProvisioningFieldValue<string>;
}

interface CatalogUserDataSourceInit {
  source: { case: 'inline'; value: string } | { case: 'secret'; value: { name: string } };
}

interface CatalogUserDataPolicyInit {
  behavior:
    | { case: 'locked'; value: CatalogUserDataSourceInit }
    | { case: 'editable'; value: { defaultValue: CatalogUserDataSourceInit } };
}

const emptyCatalogUserDataValue: CatalogUserDataValue = {
  source: 'inline',
  value: '',
};

const stringValue = (value: unknown): string => (typeof value === 'string' ? value : '');

const readCatalogUserDataValue = (value: unknown): CatalogUserDataValue => {
  if (!value || typeof value !== 'object') {
    return emptyCatalogUserDataValue;
  }

  const source = (value as { source?: unknown }).source;
  if (!source || typeof source !== 'object') {
    return emptyCatalogUserDataValue;
  }

  const sourceCase = (source as { case?: unknown }).case;
  if (sourceCase === 'inline') {
    return { source: 'inline', value: stringValue((source as { value?: unknown }).value) };
  }

  if (sourceCase === 'secret') {
    const reference = (source as { value?: unknown }).value;
    if (!reference || typeof reference !== 'object') {
      return { source: 'secret', value: '' };
    }
    const secret = reference as { id?: unknown; name?: unknown };
    return {
      source: 'secret',
      value: stringValue(secret.name) || stringValue(secret.id),
    };
  }

  return emptyCatalogUserDataValue;
};

const catalogUserDataFieldFromPolicy = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): ProvisioningFieldValue<CatalogUserDataValue> =>
  catalogValueFromPolicy(policy, readCatalogUserDataValue, emptyCatalogUserDataValue);

export const catalogUserDataValuesFromPolicy = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): CatalogUserDataFormValues => {
  const field = catalogUserDataFieldFromPolicy(policy);

  // Secret-backed policies may be created through the CLI, but the authoring wizards only support
  // text user data. Ignore the secret when loading edit values and expose an empty text field.
  return {
    userData: provisioningFieldValue(
      field.value.source === 'inline' ? field.value.value : '',
      field.locked,
    ),
  };
};

export const catalogUserDataValueFromPolicy = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): CatalogUserDataValue => catalogUserDataFieldFromPolicy(policy).value;

export const catalogUserDataDisplayValue = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): string | undefined => {
  const value = catalogUserDataValueFromPolicy(policy).value.trim();
  return value || undefined;
};

/** OSAC-6054: catalog user data is one governed source, not a scalar field. */
export const catalogUserDataPolicyFromValues = (
  values: CatalogUserDataFormValues,
): CatalogUserDataPolicyInit | undefined => {
  const value = values.userData.value.trim();
  const userDataSource: CatalogUserDataSourceInit = { source: { case: 'inline', value } };

  if (values.userData.locked) {
    return { behavior: { case: 'locked', value: userDataSource } };
  }
  if (!value) {
    return undefined;
  }
  return { behavior: { case: 'editable', value: { defaultValue: userDataSource } } };
};
