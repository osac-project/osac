import { TFunction } from 'i18next';
import * as Yup from 'yup';

import { positiveIntegerSchema } from '@osac/ui-components/validation/positive-integer';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

const requiredForGpu = (t: TFunction) => t('Required when configuring a GPU');
const isGpuConfigured = (...values: unknown[]) => values.some((value) => Boolean(value));

const gpuValidationSchema = (t: TFunction) =>
  Yup.object({
    pciDeviceSelector: Yup.string(),
    resourceName: Yup.string(),
    // No GPU-specific upper bound here; the backend enforces count <= 16.
    count: positiveIntegerSchema(t).notRequired(),
  }).test('gpu-fields-complete', requiredForGpu(t), function (gpu) {
    const originalGpu = this.originalValue as
      | { pciDeviceSelector?: unknown; resourceName?: unknown; count?: unknown }
      | undefined;
    const values = originalGpu ?? gpu;

    if (!values || !isGpuConfigured(values.pciDeviceSelector, values.resourceName, values.count)) {
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
      vcpus: positiveIntegerSchema(t).required(t('vCPUs are required')),
      memoryGib: positiveIntegerSchema(t).required(t('Memory is required')),
      gpu: gpuValidationSchema(t),
    }),
  });
