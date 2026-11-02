import type { UserDataSource } from '@osac/types';

import type { CatalogFieldPolicyLike } from './catalogItemPolicy';
import {
  type ProvisioningFieldValue,
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../ProvisioningWizard/fields/provisioningFieldValue';

export interface CatalogUserDataValue {
  source: 'inline' | 'secret';
  value: string;
}

export interface CatalogUserDataFormValues {
  userData: ProvisioningFieldValue<string>;
  /** Preserves an existing Secret source while the authoring control edits inline text. */
  userDataSecret: ProvisioningFieldValue<string>;
}

interface CatalogUserDataSourceInit {
  source: { case: 'inline'; value: string } | { case: 'secret'; value: { name: string } };
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
      value: source.value?.name || '',
    };
  }

  return emptyCatalogUserDataValue;
};

export const catalogUserDataFieldFromPolicy = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): ProvisioningFieldValue<CatalogUserDataValue> => {
  const field = getFieldFromCatalogItemPolicy<UserDataSource | undefined>(policy, undefined);
  return provisioningFieldValue(readCatalogUserDataValue(field.value), field.locked);
};

export const catalogUserDataValuesFromPolicy = (
  policy: CatalogFieldPolicyLike<UserDataSource>,
): CatalogUserDataFormValues => {
  const field = catalogUserDataFieldFromPolicy(policy);
  const { source, value } = field.value;

  return {
    userData: provisioningFieldValue(source === 'inline' ? value : '', field.locked),
    userDataSecret: provisioningFieldValue(source === 'secret' ? value : '', field.locked),
  };
};

/** OSAC-6054: catalog user data is one governed source, not a scalar field. */
export const catalogUserDataPolicyFromValues = (
  values: CatalogUserDataFormValues,
): CatalogFieldPolicyLike<CatalogUserDataSourceInit> => {
  const inlineValue = values.userData.value.trim();
  const secretValue = values.userDataSecret.value;
  const userDataSource: CatalogUserDataSourceInit = inlineValue
    ? { source: { case: 'inline', value: inlineValue } }
    : secretValue
      ? { source: { case: 'secret', value: { name: secretValue } } }
      : { source: { case: 'inline', value: '' } };
  const value = inlineValue || secretValue;

  if (values.userData.locked) {
    return { behavior: { case: 'locked', value: userDataSource } };
  }
  if (!value) {
    return undefined;
  }
  return { behavior: { case: 'editable', value: { defaultValue: userDataSource } } };
};
