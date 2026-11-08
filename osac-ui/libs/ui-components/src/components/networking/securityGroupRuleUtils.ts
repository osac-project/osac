import type { SecurityRule } from '@osac/types';

/** Strip protobuf message metadata (e.g. $typeName) so the rule can be sent back as a plain JSON body. */
export const toPlainRule = (rule: SecurityRule) => ({
  protocol: rule.protocol,
  ...(rule.portFrom !== undefined && { portFrom: rule.portFrom }),
  ...(rule.portTo !== undefined && { portTo: rule.portTo }),
  ...(rule.ipv4Cidr && { ipv4Cidr: rule.ipv4Cidr }),
});
