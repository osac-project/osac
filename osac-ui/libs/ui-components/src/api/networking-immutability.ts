import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';
import type { TFunction } from 'i18next';

import {
  BareMetalInstances,
  Clusters,
  ComputeInstances,
  ExternalIPAttachments,
  ExternalIPs,
  NATGateways,
  SecurityGroups,
  Subnets,
  VirtualNetworks,
} from '@osac/types';

const NETWORKING_SERVICE_NAMES = new Set<string>([
  VirtualNetworks.typeName,
  Subnets.typeName,
  ExternalIPs.typeName,
  ExternalIPAttachments.typeName,
  NATGateways.typeName,
  SecurityGroups.typeName,
]);

const WORKLOAD_NETWORK_FIELDS: Record<string, readonly string[]> = {
  [ComputeInstances.typeName]: ['network_attachments', 'auto_external_ip_attachment'],
  [BareMetalInstances.typeName]: ['network_attachments', 'auto_external_ip_attachment'],
  [Clusters.typeName]: ['network_attachment', 'auto_external_ip_attachment'],
};

const resourceGuidance = (t: TFunction): string =>
  t(
    'Delete and recreate the resource to change its networking. Remove dependent resources first. Tenant defaults cannot be deleted; create another resource instead.',
  );

const workloadGuidance = (t: TFunction): string =>
  t('Delete and recreate the workload to change its network attachment.');

const unsupportedRuleUpdateGuidance = (t: TFunction): string =>
  t('This server does not support updating security group rules.');

const IMMUTABLE_WORDING =
  /\bimmutable\b|\b(?:cannot|can't|can not|may not|must not|is not|are not)\s+(?:be\s+)?(?:allowed to be\s+)?(?:changed|change|modified|modify)\b/i;

const DELETE_RECREATE_GUIDANCE = /\bdelete (?:and|or) recreate\b/i;

const COMPUTE_ATTACHMENT_COUNT = /\bcannot change number of network attachments\b/i;

const toCamelCase = (field: string): string =>
  field.replace(/_([a-z])/g, (_match, letter: string) => letter.toUpperCase());

const hasExactToken = (message: string, token: string): boolean =>
  new RegExp(`(^|[^A-Za-z0-9_])${token}($|[^A-Za-z0-9_])`).test(message);

const mentionsProtectedWorkloadField = (message: string, fields: readonly string[]): boolean =>
  fields.some(
    (field) => hasExactToken(message, field) || hasExactToken(message, toCamelCase(field)),
  );

const replacementGuidance = (
  serviceTypeName: string,
  error: ConnectError,
  t: TFunction,
): string | undefined => {
  if (NETWORKING_SERVICE_NAMES.has(serviceTypeName)) {
    if (error.code === Code.Unimplemented) {
      return serviceTypeName === SecurityGroups.typeName
        ? `${unsupportedRuleUpdateGuidance(t)} ${resourceGuidance(t)}`
        : resourceGuidance(t);
    }
    if (error.code === Code.InvalidArgument && IMMUTABLE_WORDING.test(error.rawMessage)) {
      return resourceGuidance(t);
    }
    return undefined;
  }

  const workloadFields = WORKLOAD_NETWORK_FIELDS[serviceTypeName];
  if (
    workloadFields &&
    error.code === Code.InvalidArgument &&
    IMMUTABLE_WORDING.test(error.rawMessage) &&
    (mentionsProtectedWorkloadField(error.rawMessage, workloadFields) ||
      (serviceTypeName === ComputeInstances.typeName &&
        COMPUTE_ATTACHMENT_COUNT.test(error.rawMessage)))
  ) {
    return workloadGuidance(t);
  }

  return undefined;
};

/** Add replacement guidance to recognized Update failures; preserve other errors. */
export const createNetworkingImmutabilityInterceptor = (t: TFunction): Interceptor => {
  return (next) => async (request) => {
    try {
      return await next(request);
    } catch (error) {
      if (!(error instanceof ConnectError) || request.method.name !== 'Update') {
        throw error;
      }

      const guidance = replacementGuidance(request.service.typeName, error, t);
      if (guidance === undefined || DELETE_RECREATE_GUIDANCE.test(error.rawMessage)) {
        throw error;
      }

      const decorated = new ConnectError(
        `${error.rawMessage} ${guidance}`,
        error.code,
        error.metadata,
        undefined,
        error,
      );
      decorated.details = error.details;
      throw decorated;
    }
  };
};
