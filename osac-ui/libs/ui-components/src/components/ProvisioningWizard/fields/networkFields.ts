import {
  type FieldPolicyLike,
  catalogFieldPolicyLocked,
} from '../../catalog/catalogFieldPolicyDisplay';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

export interface NetworkAttachmentFormFields {
  useDefaultNetwork: boolean;
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
}

export const getEmptyNetworkAttachmentFormFields = (): NetworkAttachmentFormFields => ({
  useDefaultNetwork: true,
  virtualNetwork: emptyResourceSelectValue(),
  subnet: emptyResourceSelectValue(),
  securityGroups: [],
});

interface CatalogResourceReference {
  id?: string;
  name?: string;
}

interface CatalogNetworkAttachment {
  virtualNetwork?: CatalogResourceReference;
  subnet?: CatalogResourceReference;
  securityGroups?: CatalogResourceReference[];
}

interface CatalogNetworkAttachmentList {
  items?: CatalogNetworkAttachment[];
}

const resourceSelectValueFromReference = (
  reference?: CatalogResourceReference,
): ResourceSelectValue => ({
  id: reference?.id?.trim() ?? '',
  name: reference?.name?.trim() ?? '',
});

const attachmentFromPolicy = (
  policy: FieldPolicyLike<unknown>,
): CatalogNetworkAttachment | undefined => {
  const policyValue =
    policy?.behavior?.case === 'locked'
      ? policy.behavior.value
      : policy?.behavior?.case === 'editable'
        ? (policy.behavior.value as { defaultValue?: unknown } | undefined)?.defaultValue
        : undefined;

  if (policyValue && typeof policyValue === 'object' && 'items' in policyValue) {
    return (policyValue as CatalogNetworkAttachmentList).items?.[0];
  }
  return policyValue as CatalogNetworkAttachment | undefined;
};

export const networkAttachmentFieldsFromPolicy = (
  policy: FieldPolicyLike<unknown>,
): { value: NetworkAttachmentFormFields; locked: boolean } => {
  const attachment = attachmentFromPolicy(policy);
  const virtualNetwork = resourceSelectValueFromReference(attachment?.virtualNetwork);
  const subnet = resourceSelectValueFromReference(attachment?.subnet);
  const securityGroups = (attachment?.securityGroups ?? []).map(resourceSelectValueFromReference);
  const hasCustomAttachment = Boolean(
    virtualNetwork.id ||
    virtualNetwork.name ||
    subnet.id ||
    subnet.name ||
    securityGroups.length > 0,
  );

  return {
    value: {
      useDefaultNetwork: !hasCustomAttachment,
      virtualNetwork: hasCustomAttachment ? virtualNetwork : emptyResourceSelectValue(),
      subnet: hasCustomAttachment ? subnet : emptyResourceSelectValue(),
      securityGroups,
    },
    locked: catalogFieldPolicyLocked(policy),
  };
};
