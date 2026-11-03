import { useCallback, useEffect, useState } from 'react';
import { type MessageInitShape } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import { Cluster, ClusterSchema, Clusters, Secrets } from '@osac/types';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import { useApiFetch } from '../api-context';
import { useGetResource, useInvalidateServiceQueries, useListResource } from '../use-resource';

export const useClusters = () => {
  const filter = useProjectFilterQuery<Cluster>();
  return useListResource(Clusters, { filter }, {}, (reply) => reply.items);
};

export const useCluster = (id: string) =>
  useGetResource(Clusters, { id }, { enabled: Boolean(id) }, (reply) => reply.object);

export const useDeleteCluster = () => {
  const client = useApiFetch(Clusters);
  const invalidateServiceQueries = useInvalidateServiceQueries();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateServiceQueries(Clusters),
    retry: false,
  });
};

export const useProvisionCluster = () => {
  const client = useApiFetch(Clusters);
  const invalidateServiceQueries = useInvalidateServiceQueries();
  return useMutation({
    mutationFn: (cluster: MessageInitShape<typeof ClusterSchema>) =>
      client.create({ object: cluster }).then((r) => r.object),
    onSuccess: () => invalidateServiceQueries(Clusters),
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
