import { useCallback, useEffect, useState } from 'react';

import { type Cluster, Clusters, Secrets } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import { useApiFetch } from '../api-context';
import { useListResource } from '../use-resource';

const combineClusterListFilter = (
  projectFilter: CelFilter<Cluster> | undefined,
  filters?: CelFilter<Cluster>,
): CelFilter<Cluster> | undefined => {
  const expression = cel<Cluster>((builder) => builder.and(projectFilter, filters));
  return expression === '' ? undefined : expression;
};

export const useClusters = (
  filters?: CelFilter<Cluster>,
  enabled = true,
): {
  clusters: Cluster[];
  totalItems: number;
  isLoading: boolean;
  error: unknown;
} => {
  const projectFilter = useProjectFilterQuery<Cluster>();
  const filter = combineClusterListFilter(projectFilter, filters);
  const totalsFilter = projectFilter;

  const filteredClustersQuery = useListResource(Clusters, filter ? { filter } : {}, { enabled });
  const totalClustersQuery = useListResource(
    Clusters,
    totalsFilter ? { filter: totalsFilter, limit: 0 } : { limit: 0 },
    { enabled },
  );

  return {
    clusters: filteredClustersQuery.data?.items ?? [],
    totalItems: totalClustersQuery.data?.total ?? 0,
    isLoading: filteredClustersQuery.isLoading || totalClustersQuery.isLoading,
    error: filteredClustersQuery.error ?? totalClustersQuery.error,
  };
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
