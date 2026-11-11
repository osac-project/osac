import { useNavigate, useParams } from 'react-router-dom';
import { Alert, Bullseye, Spinner } from '@patternfly/react-core';

import { useVirtualNetwork } from '../../api/v1/networking';
import { AttachNatGatewayWizard } from '../../components/networking/AttachNatGatewayWizard';
import { NAT_GATEWAYS_LIST_PATH } from '../../components/networking/natGatewayPaths';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';

interface AttachNatGatewayWizardPageProps {
  /**
   * Set when the flow starts from the NAT gateways list: Create and Cancel return there.
   * Otherwise they return to the virtual network's NAT gateway tab.
   */
  fromNatGatewaysList?: boolean;
}

const AttachNatGatewayWizardPage = ({ fromNatGatewaysList }: AttachNatGatewayWizardPageProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const { data: virtualNetwork, isLoading, error } = useVirtualNetwork(id);
  const natGatewayTabPath = `/networking/virtual-networks/${id}?tab=nat-gateway`;

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

  const breadcrumbParents = fromNatGatewaysList
    ? [{ to: NAT_GATEWAYS_LIST_PATH, label: t('NAT gateways') }]
    : [
        { to: '/networking/virtual-networks', label: t('Virtual networks') },
        { to: natGatewayTabPath, label: virtualNetwork.metadata?.name || virtualNetwork.id },
      ];

  return (
    <AttachNatGatewayWizard
      virtualNetwork={virtualNetwork}
      breadcrumbParents={breadcrumbParents}
      onClose={() => navigate(fromNatGatewaysList ? NAT_GATEWAYS_LIST_PATH : natGatewayTabPath)}
    />
  );
};

export default AttachNatGatewayWizardPage;
