import { useNavigate, useParams } from 'react-router-dom';
import { Alert, Bullseye, Spinner } from '@patternfly/react-core';

import { VirtualNetworks } from '@osac/types';

import { useGetResource } from '../../api/use-resource';
import { AttachNatGatewayWizard } from '../../components/networking/AttachNatGatewayWizard';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';

const AttachNatGatewayWizardPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const {
    data: virtualNetworkResponse,
    isLoading,
    error,
  } = useGetResource(VirtualNetworks, { id }, { enabled: Boolean(id) });
  const virtualNetwork = virtualNetworkResponse?.object;

  if (isLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  if (error || !virtualNetwork) {
    return (
      <Alert variant="danger" isInline title={t('Failed to load virtual network')}>
        {error ? getErrorMessage(error) : t('Virtual network not found.')}
      </Alert>
    );
  }

  return (
    <AttachNatGatewayWizard
      virtualNetwork={virtualNetwork}
      onClose={() => navigate(`/networking/virtual-networks/${id}`)}
    />
  );
};

export default AttachNatGatewayWizardPage;
