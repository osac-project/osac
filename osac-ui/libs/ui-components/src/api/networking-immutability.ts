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

import { toSnakeCase } from '../utils/snakeCase';

const UPDATELESS_NETWORKING_SERVICES = new Set<string>([
  VirtualNetworks.typeName,
  Subnets.typeName,
  ExternalIPs.typeName,
  ExternalIPAttachments.typeName,
  NATGateways.typeName,
]);

const WORKLOAD_NETWORK_FIELDS: Record<string, readonly string[]> = {
  [ComputeInstances.typeName]: ['network_attachments', 'auto_external_ip_attachment'],
  [BareMetalInstances.typeName]: ['network_attachments', 'auto_external_ip_attachment'],
  [Clusters.typeName]: ['network_attachment', 'auto_external_ip_attachment'],
};

const SECURITY_GROUP_RULE_FIELDS = new Set(['ingress', 'egress']);

const NETWORKING_SERVICE_NAMES = new Set<string>([
  ...UPDATELESS_NETWORKING_SERVICES,
  SecurityGroups.typeName,
]);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const toCamelCase = (field: string): string =>
  field.replace(/_([a-z])/g, (_match, letter: string) => letter.toUpperCase());

const normalizePath = (path: string): string => path.split('.').map(toSnakeCase).join('.');

const updateMaskPaths = (request: Record<string, unknown>): string[] => {
  const updateMask = request.updateMask;
  if (!isRecord(updateMask) || !Array.isArray(updateMask.paths)) {
    return [];
  }
  return updateMask.paths.filter(
    (path): path is string => typeof path === 'string' && path.length > 0,
  );
};

const updateMaskHasPath = (
  paths: readonly string[],
  predicate: (path: string) => boolean,
): boolean => paths.some((path) => predicate(normalizePath(path)));

const hasSuppliedProperty = (record: Record<string, unknown>, keys: readonly string[]): boolean =>
  keys.some(
    (key) => Object.prototype.hasOwnProperty.call(record, key) && record[key] !== undefined,
  );

const preflightMessage = (t: TFunction): string =>
  t('Network configuration cannot be changed after creation.');

const resourceGuidance = (t: TFunction): string =>
  t(
    'Delete and recreate the resource to change its networking. Remove dependent resources first. Tenant defaults cannot be deleted; create another resource instead.',
  );

const workloadGuidance = (t: TFunction): string =>
  t('Delete and recreate the workload to change its network attachment.');

const unsupportedRuleUpdateGuidance = (t: TFunction): string =>
  t('This server does not support updating security group rules.');

const rejectUpdate = (t: TFunction, guidance: string): never => {
  throw new ConnectError(`${preflightMessage(t)} ${guidance}`, Code.FailedPrecondition);
};

const isProtectedWorkloadPath = (path: string, fields: readonly string[]): boolean =>
  path === 'spec' ||
  fields.some((field) => path === `spec.${field}` || path.startsWith(`spec.${field}.`));

const suppliedWorkloadNetworkField = (
  spec: Record<string, unknown>,
  fields: readonly string[],
): boolean => fields.some((field) => hasSuppliedProperty(spec, [field, toCamelCase(field)]));

const assertWorkloadUpdateAllowed = (
  serviceTypeName: string,
  request: Record<string, unknown>,
  t: TFunction,
): void => {
  const fields = WORKLOAD_NETWORK_FIELDS[serviceTypeName];
  if (!fields) {
    return;
  }

  const paths = updateMaskPaths(request);
  if (updateMaskHasPath(paths, (path) => isProtectedWorkloadPath(path, fields))) {
    rejectUpdate(t, workloadGuidance(t));
  }

  const object = request.object;
  if (
    isRecord(object) &&
    isRecord(object.spec) &&
    suppliedWorkloadNetworkField(object.spec, fields)
  ) {
    rejectUpdate(t, workloadGuidance(t));
  }
};

const isSecurityGroupRuleMaskPath = (path: string): boolean =>
  path === 'spec.ingress' ||
  path.startsWith('spec.ingress.') ||
  path === 'spec.egress' ||
  path.startsWith('spec.egress.');

const hasImmutableSecurityGroupFields = (object: Record<string, unknown>): boolean => {
  if (object.metadata !== undefined || object.status !== undefined) {
    return true;
  }

  const spec = object.spec;
  if (!isRecord(spec)) {
    return true;
  }

  return Object.keys(spec).some(
    (key) => key !== '$typeName' && !SECURITY_GROUP_RULE_FIELDS.has(key) && spec[key] !== undefined,
  );
};

const assertSecurityGroupUpdateAllowed = (request: Record<string, unknown>, t: TFunction): void => {
  const object = request.object;
  if (!isRecord(object) || typeof object.id !== 'string' || object.id.length === 0) {
    throw new ConnectError(preflightMessage(t), Code.FailedPrecondition);
  }
  if (!isRecord(object.spec)) {
    throw new ConnectError(preflightMessage(t), Code.FailedPrecondition);
  }

  const spec = object.spec;
  const hasRuleLists = Array.isArray(spec.ingress) && Array.isArray(spec.egress);
  if (!hasRuleLists || hasImmutableSecurityGroupFields(object)) {
    rejectUpdate(t, resourceGuidance(t));
  }

  if (updateMaskHasPath(updateMaskPaths(request), (path) => !isSecurityGroupRuleMaskPath(path))) {
    rejectUpdate(t, resourceGuidance(t));
  }
};

/** Throw a translated ConnectError before sending a forbidden public Update. */
export const assertNetworkUpdateAllowed = (
  serviceTypeName: string,
  request: unknown,
  t: TFunction,
): void => {
  if (!isRecord(request)) {
    return;
  }

  if (UPDATELESS_NETWORKING_SERVICES.has(serviceTypeName)) {
    rejectUpdate(t, resourceGuidance(t));
  }

  if (serviceTypeName === SecurityGroups.typeName) {
    assertSecurityGroupUpdateAllowed(request, t);
    return;
  }

  assertWorkloadUpdateAllowed(serviceTypeName, request, t);
};

const IMMUTABLE_WORDING =
  /\bimmutable\b|\b(?:cannot|can't|can not|may not|must not|is not|are not)\s+(?:be\s+)?(?:allowed to be\s+)?(?:changed|change|modified|modify)\b/i;

const DELETE_RECREATE_GUIDANCE = /\bdelete (?:and|or) recreate\b/i;

const COMPUTE_ATTACHMENT_COUNT = /\bcannot change number of network attachments\b/i;

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
