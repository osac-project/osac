import { TFunction } from 'i18next';
import * as Yup from 'yup';

import { intSchema } from '@osac/ui-components/validation/number';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { InstanceTypeCreateFormValues } from './values';

const requiredForGpu = (t: TFunction) => t('Required when configuring a GPU');

type GpuFormValues = InstanceTypeCreateFormValues['spec']['gpu'];

export const isGpuConfigured = (gpu: GpuFormValues | undefined) =>
  Boolean(gpu?.pciDeviceSelector || gpu?.resourceName || gpu?.count);

const gpuValidationSchema = (t: TFunction) =>
  Yup.object({
    pciDeviceSelector: Yup.string(),
    resourceName: Yup.string(),
    count: intSchema(t, 1, 16),
  }).test('gpu-fields-complete', requiredForGpu(t), function () {
    const values = this.originalValue as GpuFormValues | undefined;

    if (!values || !isGpuConfigured(values)) {
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
      gpu: Yup.lazy((gpu: GpuFormValues | undefined) =>
        isGpuConfigured(gpu) ? gpuValidationSchema(t) : Yup.mixed(),
      ),
    }),
  });
