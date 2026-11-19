import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Button,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
} from '@patternfly/react-core';

import { SubnetState, Subnets, VirtualNetworks } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal';

import { useDeleteResource, useGetResource } from '../../api/use-resource';
import { isDefaultNetworkingResource, resourceDisplayName } from '../../api/v1/networking';
import { SubnetStatusLabel } from '../../components/networking/SubnetStatusLabel';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { useTranslation } from '../../hooks/useTranslation';
import { defaultResourceDeleteReason } from '../../utils/networking';

export const SubnetDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);

  const { data: subnetResponse, isLoading, error } = useGetResource(Subnets, { id });
  const subnet = subnetResponse?.object;
  const virtualNetworkId = subnet?.spec?.virtualNetwork?.id ?? '';
  const { data: virtualNetworkResponse } = useGetResource(
    VirtualNetworks,
    { id: virtualNetworkId },
    { enabled: Boolean(virtualNetworkId) },
  );
  const virtualNetwork = virtualNetworkResponse?.object;
  const deleteSubnet = useDeleteResource(Subnets);

  const subnetName = resourceDisplayName(subnet?.metadata, id);
  const virtualNetworkName = resourceDisplayName(virtualNetwork?.metadata, virtualNetworkId);
  const isFailed = subnet?.status?.state === SubnetState.FAILED;
  const isDefault = isDefaultNetworkingResource(subnet);
  const canDelete = Boolean(subnet) && !isDefault;
  const parentPath = virtualNetworkId
    ? `/networking/virtual-networks/${virtualNetworkId}`
    : '/networking/virtual-networks';

  return (
    <>
      <ListPage
        title={subnetName}
        description={t('View the subnet configuration and related resources.')}
        actions={
          <DeleteResourceButton
            canDelete={canDelete}
            disabledReason={isDefault ? defaultResourceDeleteReason(t) : undefined}
            onClick={() => {
              if (canDelete) {
                setIsDeleteModalOpen(true);
              }
            }}
          />
        }
        breadcrumb={
          <Breadcrumb>
            <BreadcrumbItem>
              <Button
                variant="link"
                isInline
                onClick={() => navigate('/networking/virtual-networks')}
              >
                {t('Virtual networks')}
              </Button>
            </BreadcrumbItem>
            {virtualNetworkId && (
              <BreadcrumbItem>
                <Button variant="link" isInline onClick={() => navigate(parentPath)}>
                  {virtualNetworkName}
                </Button>
              </BreadcrumbItem>
            )}
            <BreadcrumbItem isActive>{subnetName}</BreadcrumbItem>
          </Breadcrumb>
        }
      >
        <ListPageBody isLoading={isLoading} error={error}>
          {isFailed && subnet?.status?.message && (
            <Alert variant="danger" title={t('Provisioning failed')} isInline>
              {subnet.status.message}
            </Alert>
          )}

          <Stack hasGutter>
            <Card>
              <CardTitle>{t('Details')}</CardTitle>
              <CardBody>
                <DescriptionList isHorizontal>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
                    <DescriptionListDescription>{subnetName}</DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Virtual Network')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {virtualNetworkId ? (
                        <Button variant="link" isInline onClick={() => navigate(parentPath)}>
                          {virtualNetworkName}
                        </Button>
                      ) : (
                        virtualNetworkName
                      )}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('IPv4 CIDR')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {subnet?.spec?.ipv4Cidr ?? '—'}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      <SubnetStatusLabel state={subnet?.status?.state} />
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  {subnet?.status?.message && !isFailed && (
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Message')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        {subnet.status.message}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  )}
                </DescriptionList>
              </CardBody>
            </Card>
          </Stack>
        </ListPageBody>
      </ListPage>

      {isDeleteModalOpen && canDelete && (
        <DeleteResourceModal
          resourceName={subnetName}
          label={t('This will permanently delete the subnet. This action cannot be undone.')}
          errorLabel={t('Failed to delete subnet')}
          onClose={() => setIsDeleteModalOpen(false)}
          onSuccess={() => navigate(parentPath)}
          mutation={deleteSubnet}
          variables={{ id }}
        />
      )}
    </>
  );
};
