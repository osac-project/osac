import type { ProvisioningFieldValue } from './provisioningFieldValue';
import { provisioningFieldFromPolicy, provisioningFieldValue } from './provisioningFieldValue';
import type { FieldPolicyLike } from '../../catalog/catalogFieldPolicyDisplay';
import { catalogFieldPolicyLocked } from '../../catalog/catalogFieldPolicyDisplay';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

interface AttachmentLike {
  subnet?: { id?: string; name?: string };
  virtualNetwork?: { id?: string; name?: string };
  securityGroups?: { id?: string; name?: string }[];
}

export interface NetworkAttachmentFormFields {
  useDefaultNetwork: ProvisioningFieldValue<boolean>;
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
}

export const boolFieldFromPolicy = (
  policy?: FieldPolicyLike & {
    behavior?: { case?: string; value?: boolean | { defaultValue?: boolean } };
  },
  fallback = false,
): ProvisioningFieldValue<boolean> => {
  const value =
    policy?.behavior?.case === 'editable'
      ? Boolean(
          (policy.behavior.value as { defaultValue?: boolean } | undefined)?.defaultValue ??
          fallback,
        )
      : policy?.behavior?.case === 'locked'
        ? Boolean(policy.behavior.value)
        : fallback;
  return provisioningFieldFromPolicy(value, policy);
};

export const networkAttachmentFieldsFromPolicy = (
  policy: FieldPolicyLike,
  attachment?: AttachmentLike,
): NetworkAttachmentFormFields => {
  const locked = catalogFieldPolicyLocked(policy);
  const subnetId = attachment?.subnet?.id?.trim() ?? '';
  const subnetName = attachment?.subnet?.name?.trim() ?? '';
  const virtualNetworkId = attachment?.virtualNetwork?.id?.trim() ?? '';
  const virtualNetworkName = attachment?.virtualNetwork?.name?.trim() ?? '';
  const hasCustomAttachment = Boolean(subnetId || subnetName);

  return {
    useDefaultNetwork: provisioningFieldValue(!hasCustomAttachment, locked),
    virtualNetwork:
      virtualNetworkId || virtualNetworkName
        ? { id: virtualNetworkId, name: virtualNetworkName }
        : emptyResourceSelectValue(),
    subnet: hasCustomAttachment ? { id: subnetId, name: subnetName } : emptyResourceSelectValue(),
    securityGroups: (attachment?.securityGroups ?? []).map((group) => ({
      id: group.id ?? '',
      name: group.name ?? '',
    })),
  };
};

export const catalogAttachmentFromPolicy = <T>(
  policy:
    | {
        behavior?:
          | { case: 'locked'; value: T }
          | { case: 'editable'; value: { defaultValue?: T } }
          | { case?: undefined; value?: undefined };
      }
    | undefined,
): T | undefined => {
  if (policy?.behavior?.case === 'locked') {
    return policy.behavior.value;
  }
  if (policy?.behavior?.case === 'editable') {
    return policy.behavior.value.defaultValue;
  }
  return undefined;
};
