import { type FormikErrors } from 'formik';
import { type TFunction } from 'i18next';
import * as Yup from 'yup';

import {
  isKubernetesLabelKey,
  isKubernetesLabelValue,
} from '@osac/ui-components/validation/kubernetes-label';
import { bigIntSchema, intSchema } from '@osac/ui-components/validation/number';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { BareMetalInstanceTypeFormValues } from './values';
import type { KeyValuePair } from '../../Form/KeyValueMapField';

const hostLabelSelectorSchema = (t: TFunction) => {
  const requiredOrDuplicateKeyMessage = t('Host label keys are required and must be unique');
  const invalidKeyMessage = t('Host label key must be a valid Kubernetes label');
  const invalidValueMessage = t('Host label value must be a valid Kubernetes label value');

  return Yup.array().test('valid-host-labels', requiredOrDuplicateKeyMessage, (pairs, context) => {
    const keys = (pairs as KeyValuePair[] | undefined) ?? [];
    const seenKeys = new Set<string>();

    if (keys.length === 0) {
      return context.createError({ message: requiredOrDuplicateKeyMessage });
    }

    for (const [index, pair] of keys.entries()) {
      const key = pair.key;
      if (!key || seenKeys.has(key)) {
        return context.createError({
          path: `${context.path}[${index}].key`,
          message: requiredOrDuplicateKeyMessage,
        });
      }

      if (!isKubernetesLabelKey(key)) {
        return context.createError({
          path: `${context.path}[${index}].key`,
          message: invalidKeyMessage,
        });
      }

      if (!isKubernetesLabelValue(pair.value ?? '')) {
        return context.createError({
          path: `${context.path}[${index}].value`,
          message: invalidValueMessage,
        });
      }

      seenKeys.add(key);
    }

    return true;
  });
};

export const getBareMetalInstanceTypeSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    spec: Yup.object({
      description: Yup.string(),
      cpu: Yup.object({
        cores: intSchema(t).required(t('Cores are required')),
        architecture: Yup.string().required(t('Architecture is required')),
        model: Yup.string(),
        threadsPerCore: intSchema(t).required(t('Threads per core are required')),
      }),
      memory: Yup.object({
        totalGb: bigIntSchema(t).required(t('Total (GB) is required')),
        type: Yup.string(),
      }),
      disks: Yup.array().of(
        Yup.object({
          type: Yup.string().required(t('Type is required')),
          capacityGb: bigIntSchema(t).required(t('Capacity (GB) is required')),
          interface: Yup.string().required(t('Interface is required')),
        }),
      ),
      accelerators: Yup.array().of(
        Yup.object({
          type: Yup.string().required(t('Type is required')),
          model: Yup.string().required(t('Model is required')),
          vendor: Yup.string(),
          memoryGb: intSchema(t),
        }),
      ),
      networkPorts: Yup.array().of(
        Yup.object({
          name: Yup.string().required(t('Name is required')),
          role: Yup.string().required(t('Role is required')),
          type: Yup.string().required(t('Type is required')),
          speed: Yup.string().required(t('Speed is required')),
        }),
      ),
      capabilities: Yup.array(),
      hostLabelSelector: hostLabelSelectorSchema(t),
    }),
  });

export const BAREMETAL_STEP_IDS = [
  'general',
  'cpu-memory',
  'accelerators',
  'disks',
  'networking',
  'capabilities',
  'review',
] as const;

export type BareMetalStepId = (typeof BAREMETAL_STEP_IDS)[number];

/** Reports whether the given wizard step holds any Formik validation errors. */
export const bareMetalStepHasErrors = (
  stepId: string,
  errors: FormikErrors<BareMetalInstanceTypeFormValues>,
): boolean => {
  const spec = errors.spec;
  switch (stepId) {
    case 'general':
      return Boolean(errors.metadata?.name || spec?.description || spec?.hostLabelSelector);
    case 'cpu-memory':
      return Boolean(spec?.cpu || spec?.memory);
    case 'accelerators':
      return Boolean(spec?.accelerators);
    case 'disks':
      return Boolean(spec?.disks);
    case 'networking':
      return Boolean(spec?.networkPorts);
    case 'capabilities':
      return Boolean(spec?.capabilities);
    case 'review':
      return false;
    default:
      return false;
  }
};
