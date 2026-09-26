import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';
import { userDataSchema } from '@osac/ui-components/validation/user-data';

import {
  getCatalogFieldOverlay,
  hasCatalogFieldDefinition,
  mergeCatalogValidation,
  readCatalogFieldDefinitions,
} from '../../catalogOverlay';
import type { WizardStepId } from '../../stepIds';

const storageTierSchema = (t: TFunction) =>
  yup
    .object({
      id: yup.string(),
      name: yup.string(),
    })
    .test('storage-tier-id-or-name', t('Storage tier is required'), (value) => {
      const id = value?.id?.trim() ?? '';
      const name = value?.name?.trim() ?? '';
      return Boolean(id || name);
    });

const buildComputeInstanceFieldDefinitions = (catalogItem: unknown, t: TFunction) => {
  const definitions = readCatalogFieldDefinitions(catalogItem);

  const userDataOverlay = getCatalogFieldOverlay(
    'spec.user_data',
    definitions,
    t('catalogProvision.vm.fields.userData'),
  );
  const bootDiskOverlay = getCatalogFieldOverlay(
    'spec.boot_disk.size_gib',
    definitions,
    t('catalogProvision.vm.fields.bootDisk'),
  );
  const userDataRequired = hasCatalogFieldDefinition('spec.user_data', definitions);

  return {
    catalogItemId: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
    metadataName: resourceNameSchema(t),
    specInstanceType: yup.string().required(t('catalogProvision.validation.instanceTypeRequired')),
    specUserData: mergeCatalogValidation(
      userDataSchema(t),
      userDataOverlay,
      userDataRequired,
      t('catalogProvision.validation.required'),
    ),
    specBootDisk: yup.object({
      sizeGib: mergeCatalogValidation(
        yup
          .string()
          .test(
            'boot-disk-number',
            t('catalogProvision.validation.bootDiskNumber'),
            (value) => !value?.trim() || !Number.isNaN(Number(value)),
          ),
        bootDiskOverlay,
        true,
        t('catalogProvision.validation.required'),
      ),
      storageTier: storageTierSchema(t),
    }),
    specAdditionalDisks: yup.array(
      yup.object({
        sizeGib: yup
          .string()
          .test('additional-disk-number', t('Additional disk size must be a number'), (value) => {
            const size = Number(value?.trim());
            return Number.isInteger(size) && size >= 1 && size <= 16384;
          }),
        storageTier: storageTierSchema(t),
      }),
    ),
    specNetworking: yup.object({
      virtualNetwork: yup
        .string()
        .required(t('catalogProvision.validation.virtualNetworkRequired')),
      subnet: yup.string().required(t('catalogProvision.validation.subnetRequired')),
      securityGroups: yup.array().min(1, t('catalogProvision.validation.securityGroupRequired')),
    }),
  };
};

/**
 * Builds a Yup schema for one wizard step only.
 *
 * Formik always validates the full form values against `validationSchema`. If this
 * included every step's fields, blur and Next would fail on steps the user has not
 * reached yet (for example, empty networking while still on General). Returning
 * only the active step's fields keeps validation scoped to the current step.
 */
export const buildComputeInstanceStepSchema = (
  catalogItem: unknown,
  stepId: WizardStepId,
  t: TFunction,
): yup.AnyObjectSchema | undefined => {
  // Review has no editable fields; Create provisions without Formik validation.
  if (stepId === 'review') {
    return undefined;
  }

  const fields = buildComputeInstanceFieldDefinitions(catalogItem, t);

  switch (stepId) {
    case 'catalog':
      return yup.object({
        catalogItemId: fields.catalogItemId,
      });
    case 'general':
      return yup.object({
        metadata: yup.object({
          name: fields.metadataName,
        }),
        spec: yup.object({
          sshKey: yup.object({ name: yup.string() }),
        }),
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
          instanceType: fields.specInstanceType,
          userData: fields.specUserData,
        }),
      });
    case 'storage':
      return yup.object({
        spec: yup.object({
          bootDisk: fields.specBootDisk,
          additionalDisks: fields.specAdditionalDisks,
        }),
      });
    case 'networking':
      return yup.object({
        spec: yup.object({
          networking: fields.specNetworking,
        }),
      });
    default:
      return undefined;
  }
};
