import { ExternalIPAttachments, ExternalIPs } from '@osac/types';

import { useApiFetch } from '../api-context';
import { type ListParams, apiQueryKey } from '../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';
import { useCreateResource, useDeleteResource, useInvalidateServiceQueries } from '../use-resource';

type ExternalIPQueryOptions = {
  enabled?: boolean;
};

export const useExternalIPs = (params: ListParams = {}, options: ExternalIPQueryOptions = {}) => {
  const client = useApiFetch(ExternalIPs);
  return useApiQuery({
    queryKey: apiQueryKey('v1/external_ips', undefined, params),
    queryFn: () => client.list(params),
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

export const useExternalIPAttachments = (
  params: ListParams = {},
  options: ExternalIPQueryOptions = {},
) => {
  const client = useApiFetch(ExternalIPAttachments);
  return useApiQuery({
    queryKey: apiQueryKey('v1/external_ip_attachments', undefined, params),
    queryFn: () => client.list(params),
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

export const generateExternalIpAttachmentName = (): string => `eipa-${crypto.randomUUID()}`;

const invalidateExternalIPAttachmentCaches = async (
  qc: ApiQueryClient,
  invalidateServiceQueries: ReturnType<typeof useInvalidateServiceQueries>,
) => {
  await Promise.all([
    qc.invalidateQueries({ queryKey: apiQueryKey('v1/external_ip_attachments') }),
    invalidateServiceQueries(ExternalIPs),
  ]);
};

export const useCreateExternalIPAttachment = () => {
  const qc = useApiQueryClient();
  const invalidateServiceQueries = useInvalidateServiceQueries();

  return useCreateResource(ExternalIPAttachments, {
    onSuccess: async () => {
      await invalidateExternalIPAttachmentCaches(qc, invalidateServiceQueries);
    },
  });
};

export const useDeleteExternalIPAttachment = () => {
  const qc = useApiQueryClient();
  const invalidateServiceQueries = useInvalidateServiceQueries();

  return useDeleteResource(ExternalIPAttachments, {
    onSuccess: async () => {
      await invalidateExternalIPAttachmentCaches(qc, invalidateServiceQueries);
    },
  });
};
