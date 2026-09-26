import { useCallback, useEffect, useState } from 'react';
import { type MessageInitShape } from '@bufbuild/protobuf';
import { keepPreviousData, useMutation } from '@tanstack/react-query';

import { Cluster, ClusterSchema, Clusters, Secrets } from '@osac/types';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import { useExternalIPAttachments, useExternalIPs } from './external-ip';
import { useApiFetch } from '../api-context';
import { type CelFilter, escapeCelStringLiteral } from '../cel';
import { apiQueryKey } from '../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';

export const useClusters = () => {
  const client = useApiFetch(Clusters);
  const filter = useProjectFilterQuery<Cluster>();
  return useApiQuery({
    queryKey: apiQueryKey('v1/clusters', undefined, filter ? { filter } : undefined),
    queryFn: () => client.list({ filter }),
    select: (data) => data.items,
    placeholderData: keepPreviousData,
  });
};

export const useCluster = (id: string) => {
  const client = useApiFetch(Clusters);
  const trimmedId = id?.trim() ?? '';
  return useApiQuery({
    queryKey: apiQueryKey('v1/clusters', [trimmedId]),
    queryFn: () => client.get({ id: trimmedId }),
    select: (data) => data.object,
    enabled: Boolean(trimmedId),
  });
};

export const invalidateClustersQueries = async (qc: ApiQueryClient) => {
  await qc.invalidateQueries({ queryKey: apiQueryKey('v1/clusters') });
};

export const useDeleteCluster = () => {
  const client = useApiFetch(Clusters);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateClustersQueries(qc),
    retry: false,
  });
};

export const useProvisionCluster = () => {
  const client = useApiFetch(Clusters);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (cluster: MessageInitShape<typeof ClusterSchema>) =>
      client.create({ object: cluster }).then((r) => r.object),
    onSuccess: async () => {
      await invalidateClustersQueries(qc);
    },
    retry: false,
  });
};

const triggerDownload = (content: string, filename: string) => {
  const blob = new Blob([content], { type: 'application/octet-stream' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
};

export const useDownloadKubeconfig = () => {
  const client = useApiFetch(Secrets);
  const [isPending, setIsPending] = useState(false);
  const [error, setError] = useState<unknown>();

  const download = useCallback(
    async (kubeconfigSecretId: string, clusterName: string) => {
      setIsPending(true);
      setError(undefined);
      try {
        const resp = await client.get({ id: kubeconfigSecretId });
        const bytes = resp.object?.data.kubeconfig;
        if (!bytes) {
          throw new Error('Kubeconfig secret is missing kubeconfig data');
        }
        triggerDownload(new TextDecoder().decode(bytes), `${clusterName}-kubeconfig.yaml`);
      } catch (e) {
        setError(e);
      } finally {
        setIsPending(false);
      }
    },
    [client],
  );

  return { download, isPending, error, setError };
};

export const useFetchClusterPassword = (passwordSecretId: string) => {
  const client = useApiFetch(Secrets);
  const [isPending, setIsPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [password, setPassword] = useState<string>();
  const [resetId, setResetId] = useState<number>(0);

  useEffect(() => {
    if (!passwordSecretId) {
      setPassword(undefined);
      return;
    }

    const controller = new AbortController();
    (async () => {
      setIsPending(true);
      setError(undefined);
      try {
        const resp = await client.get({ id: passwordSecretId }, { signal: controller.signal });
        const bytes = resp.object?.data.password;
        setPassword(bytes ? new TextDecoder().decode(bytes) : undefined);
      } catch (e) {
        if (!controller.signal.aborted) {
          setError(e);
        }
      } finally {
        if (!controller.signal.aborted) {
          setIsPending(false);
        }
      }
    })();
  }, [client, resetId, passwordSecretId]);

  const retry = useCallback(() => {
    setPassword(undefined);
    setResetId((id) => id + 1);
  }, []);

  return { password, isPending, error, retry };
};

const autoCreatedForFilter = (clusterId: string): CelFilter =>
  `this.metadata.labels["auto-created-for"] == "${escapeCelStringLiteral(clusterId)}"` as CelFilter;

export const useClusterAutoProvisionedResources = (clusterId: string, enabled: boolean) => {
  const filter = autoCreatedForFilter(clusterId);

  const externalIpsQuery = useExternalIPs({ filter, limit: 2 }, { enabled });
  const externalIpAttachmentsQuery = useExternalIPAttachments({ filter, limit: 2 }, { enabled });

  return {
    externalIps: externalIpsQuery.data ?? [],
    externalIpAttachments: externalIpAttachmentsQuery.data ?? [],
    isLoading: externalIpsQuery.isLoading || externalIpAttachmentsQuery.isLoading,
    error: externalIpsQuery.error ?? externalIpAttachmentsQuery.error,
  };
};
