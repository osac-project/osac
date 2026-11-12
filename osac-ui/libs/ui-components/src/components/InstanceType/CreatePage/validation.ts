import { TFunction } from 'i18next';
import * as Yup from 'yup';

import { intSchema } from '@osac/ui-components/validation/number';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

const requiredForGpu = (t: TFunction) => t('Required when configuring a GPU');

const gpuValidationSchema = (t: TFunction) =>
  Yup.object({
    pciDeviceSelector: Yup.string(),
    resourceName: Yup.string(),
    count: intSchema(t, 1, 16),
  }).test('gpu-fields-complete', requiredForGpu(t), function (values) {
    if (!values || !(values.pciDeviceSelector || values.resourceName || values.count)) {
      return true;
    }

    const missingFields: Array<'pciDeviceSelector' | 'resourceName' | 'count'> = [];
    if (!values.pciDeviceSelector) {
      missingFields.push('pciDeviceSelector');
    }
    if (!values.resourceName) {
      missingFields.push('resourceName');
    }
    if (!values.count) {
      missingFields.push('count');
    }

    if (missingFields.length === 0) {
      return true;
    }

    return new Yup.ValidationError(
      missingFields.map((field) =>
        this.createError({
          path: `${this.path}.${field}`,
          message: requiredForGpu(t),
        }),
      ),
    );
  });

export const getInstanceTypeCreateSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    spec: Yup.object({
      description: Yup.string(),
      vcpus: intSchema(t).required(t('vCPUs are required')),
      memoryGib: intSchema(t).required(t('Memory is required')),
      gpu: gpuValidationSchema(t),
    }),
  });
