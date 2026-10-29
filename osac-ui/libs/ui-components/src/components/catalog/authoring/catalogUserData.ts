import { type CatalogFieldPolicyLike, catalogValueFromPolicy } from './catalogItemPolicy';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface CatalogUserDataValue {
  source: 'inline' | 'secret';
  value: string;
}

export interface CatalogUserDataFormValues {
  userData: ProvisioningFieldValue<string>;
  userDataSource: ProvisioningFieldValue<'inline' | 'secret'>;
  userDataSecret: ProvisioningFieldValue<string>;
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
  policy: CatalogFieldPolicyLike,
): ProvisioningFieldValue<CatalogUserDataValue> =>
  catalogValueFromPolicy(policy, readCatalogUserDataValue, emptyCatalogUserDataValue);

export const catalogUserDataValuesFromPolicy = (
  policy: CatalogFieldPolicyLike,
): CatalogUserDataFormValues => {
  const field = catalogUserDataFieldFromPolicy(policy);
  const { source, value } = field.value;

  return {
    userDataSource: provisioningFieldValue(source, field.locked),
    userData: provisioningFieldValue(
      source === 'inline' ? value : '',
      source === 'inline' && field.locked,
    ),
    userDataSecret: provisioningFieldValue(
      source === 'secret' ? value : '',
      source === 'secret' && field.locked,
    ),
  };
};

export const catalogUserDataValueFromPolicy = (
  policy: CatalogFieldPolicyLike,
): CatalogUserDataValue => catalogUserDataFieldFromPolicy(policy).value;

export const catalogUserDataDisplayValue = (policy: CatalogFieldPolicyLike): string | undefined => {
  const value = catalogUserDataValueFromPolicy(policy).value.trim();
  return value || undefined;
};

/** OSAC-6054: catalog user data is one governed source, not a scalar field. */
export const catalogUserDataPolicyFromValues = (
  values: CatalogUserDataFormValues,
): CatalogUserDataPolicyInit | undefined => {
  const source = values.userDataSource.value === 'secret' ? 'secret' : 'inline';
  const value =
    source === 'secret' ? values.userDataSecret.value.trim() : values.userData.value.trim();
  const userDataSource: CatalogUserDataSourceInit =
    source === 'secret'
      ? { source: { case: 'secret', value: { name: value } } }
      : { source: { case: 'inline', value } };

  if (values.userData.locked) {
    return { behavior: { case: 'locked', value: userDataSource } };
  }
  if (!value) {
    return undefined;
  }
  return { behavior: { case: 'editable', value: { defaultValue: userDataSource } } };
};
