import { ExternalIPAttachments, ExternalIPs } from '@osac/types';

import { useApiFetch } from '../api-context';
import { type ListParams, apiQueryKey } from '../types';
import { useApiQuery } from '../use-api-query';

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
