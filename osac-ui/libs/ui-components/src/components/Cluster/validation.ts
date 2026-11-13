import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { intSchema } from '@osac/ui-components/validation/number';

import { cidrsOverlap } from '../../validation/cidr-validation';
import { resourceNameSchema } from '../../validation/resource-name';
interface CidrNetworkValues {
  podCidr?: { value?: string };
  serviceCidr?: { value?: string };
}

interface NodeSetWithName {
  name?: string | null;
}

export const clusterNodeSetRowSchema = (t: TFunction) =>
  Yup.object({
    rowId: Yup.string().required(),
    name: resourceNameSchema(t),
    hostType: Yup.object({
      id: Yup.string().required(t('Host type is required')),
      name: Yup.string(),
    }),
    size: intSchema(t).required(t('Pool size is required')),
  });

export const clusterNetworkDoNotOverlapTest = (
  t: TFunction,
): Yup.TestFunction<CidrNetworkValues | undefined> =>
  function validateClusterNetworkCidrsDoNotOverlap(network) {
    const podCidr = network?.podCidr?.value ?? '';
    const serviceCidr = network?.serviceCidr?.value ?? '';
    if (!cidrsOverlap(podCidr, serviceCidr, 'ipv4')) {
      return true;
    }
    return this.createError({
      path: `${this.path ? `${this.path}.` : ''}serviceCidr.value`,
      message: t('Service CIDR must not overlap the pod CIDR.'),
    });
  };

export const uniqueNodeSetNamesTest = (
  t: TFunction,
): Yup.TestFunction<NodeSetWithName[] | undefined> =>
  function validateUniqueNodeSetNames(rows) {
    const seenNames = new Set<string>();
    for (const [rowIndex, row] of (rows ?? []).entries()) {
      const name = row?.name?.trim() ?? '';
      if (!name) {
        continue;
      }
      if (seenNames.has(name)) {
        return this.createError({
          path: `${this.path}[${rowIndex}].name`,
          message: t('Node set names must be unique'),
        });
      }
      seenNames.add(name);
    }
    return true;
  };
