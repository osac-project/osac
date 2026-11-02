import type { UserDataSource } from '@osac/types';

import type { CatalogFieldPolicyLike } from './catalogItemPolicy';
import {
  type ProvisioningFieldValue,
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

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

const readCatalogUserDataValue = (value: UserDataSource | undefined): CatalogUserDataValue => {
  const source = value?.source;
  if (!source || source.case === undefined) {
    return emptyCatalogUserDataValue;
  }

  if (source.case === 'inline') {
    return { source: 'inline', value: source.value ?? '' };
  }

  if (source.case === 'secret') {
    return {
      source: 'secret',
      value: source.value?.name || source.value?.id || '',
    };
  }

  return emptyCatalogUserDataValue;
};

const catalogUserDataFieldFromPolicy = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): ProvisioningFieldValue<CatalogUserDataValue> => {
  const field = getFieldFromCatalogItemPolicy<UserDataSource | undefined>(policy, undefined);
  return provisioningFieldValue(readCatalogUserDataValue(field.value), field.locked);
};

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
