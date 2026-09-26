import type { FormikHelpers } from 'formik';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import type { ComputeInstanceWizardValues } from './fields';

/** Apply a locked SSH Secret or editable catalog default to the general step. */
export const applyVmCatalogGeneralDefaults = (
  catalogItem: ComputeInstanceCatalogItem,
  helpers: FormikHelpers<ComputeInstanceWizardValues>,
): void => {
  const behavior = catalogItem.fields?.sshKey?.behavior;
  const reference =
    behavior?.case === 'locked'
      ? behavior.value
      : behavior?.case === 'editable'
        ? behavior.value.defaultValue
        : undefined;

  if (reference?.name) {
    void helpers.setFieldValue('spec.sshKey.name', reference.name);
  }
};
