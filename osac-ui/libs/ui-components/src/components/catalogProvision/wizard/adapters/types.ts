import type { ComponentType } from 'react';
import type { FormikHelpers } from 'formik';
import type { AnyObjectSchema } from 'yup';

import type { CatalogItem } from '../../../catalog/catalogItemDisplay';
import type { CatalogProvisionKind } from '../../catalogFieldDefinition';
import type { WizardStepId } from '../stepIds';

export interface CatalogItemsQueryResult<TItem extends CatalogItem> {
  data: TItem[];
  isPending: boolean;
  isError: boolean;
  refetch: () => void;
}

export interface GeneralFieldDescriptor {
  name: string;
  labelKey: string;
  /** Catalog `display_name` override; falls back to `t(labelKey)`. */
  label?: string;
  multiline?: boolean;
  isRequired?: boolean;
  isPassword?: boolean;
  isDisabled?: boolean;
  /** i18n key for InputField helperText (e.g. RFC 1035 DNS label description). */
  helperTextKey?: string;
}

export interface CatalogProvisionAdapter<TItem extends CatalogItem, TValues, TPayload> {
  kind: CatalogProvisionKind;
  useCatalogItems: () => CatalogItemsQueryResult<TItem>;
  getInitialValues: (catalogItem: TItem | null) => TValues;
  buildCreatePayload: (values: TValues, catalogItem: TItem) => TPayload;
  ConfigurationStep: ComponentType<{ catalogItem: TItem | null }>;
  /** Optional — only kinds whose ordered steps include 'storage' (compute instances) set this. */
  StorageStep?: ComponentType<{ catalogItem: TItem | null }>;
  /** Optional — only compute instances include an access step after networking. */
  AccessStep?: ComponentType<{ catalogItem: TItem | null }>;
  NetworkingStep: ComponentType<{ catalogItem: TItem | null }>;
  GeneralStep: ComponentType<{ catalogItem: TItem | null }>;
  ReviewStep: ComponentType<{ catalogItem: TItem | null }>;
  getStepValidationSchema: (
    catalogItem: TItem | null,
    stepId: WizardStepId,
  ) => AnyObjectSchema | undefined;
  onCatalogItemSelected?: (item: TItem, helpers: FormikHelpers<TValues>) => void | Promise<void>;
  wizardTitleKey: string;
  wizardDescriptionKey: string;
  breadcrumbCreateLabelKey: string;
  ariaLabelKey: string;
}
