import { ReactNode, useCallback, useMemo } from 'react';
import { Alert, AlertActionLink, Modal } from '@patternfly/react-core';

import { useDownloadKubeconfig } from '@osac/ui-components/api/v1/cluster';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

export const useDownloadKubeconfigHandler = (clusterName: string, kubeconfigSecretId: string) => {
  const { t } = useTranslation();
  const { download, isPending, error, setError } = useDownloadKubeconfig();

  const onDownload = useCallback(
    () => download(kubeconfigSecretId, clusterName),
    [clusterName, download, kubeconfigSecretId],
  );

  const DownloadErrorModal: ReactNode = useMemo(() => {
    if (!error) {
      return null;
    }

    return (
      <Modal
        variant="small"
        isOpen
        onClose={() => setError(undefined)}
        title={t('Failed to download kubeconfig')}
      >
        <Alert
          variant="danger"
          title={t('Failed to download kubeconfig')}
          isInline
          actionLinks={
            <AlertActionLink onClick={() => download(kubeconfigSecretId, clusterName)}>
              {t('Retry')}
            </AlertActionLink>
          }
        >
          {getErrorMessage(error)}
        </Alert>
      </Modal>
    );
  }, [clusterName, download, error, kubeconfigSecretId, setError, t]);

  return { onDownload, isPending, DownloadErrorModal };
};
