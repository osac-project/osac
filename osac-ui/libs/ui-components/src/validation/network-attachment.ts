import type { TFunction } from 'i18next';
import * as yup from 'yup';

/**
 * Shared Yup schema building blocks for the VN → Subnet → SecurityGroups
 * network-attachment fields used by the cluster, VM, and bare-metal
 * provisioning adapters.
 *
 * Each adapter wires the conditional logic (`useDefaultNetwork` / `useDefaults`)
 * around its own form structure; the field-level schemas stay consistent.
 */
export const buildNetworkAttachmentSchemas = (t: TFunction) => {
  const optionalResourceSelect = yup.object({ id: yup.string(), name: yup.string() });
  const securityGroupsSchema = yup.array().of(optionalResourceSelect);

  const requiredVirtualNetwork = yup.object({
    id: yup.string().required(t('Virtual network is required')),
    name: yup.string(),
  });

  const requiredSubnet = yup.object({
    id: yup.string().required(t('Subnet is required')),
    name: yup.string(),
  });

  return {
    requiredVirtualNetwork,
    requiredSubnet,
    optionalResourceSelect,
    securityGroupsSchema,
  };
};
