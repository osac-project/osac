import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
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
  Grid,
  GridItem,
} from '@patternfly/react-core';

import { BareMetalInstanceType, BareMetalInstanceTypes } from '@osac/types/private';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import { displayValue } from '@osac/ui-components/utils/detailFormatters';

import { BAREMETAL_INSTANCE_TYPES_LIST_ROUTE } from './AdminBareMetalInstanceTypeListPage';
import BareMetalInstanceTypeDeleteModal from './BareMetalInstanceTypeDeleteModal';
import { useTranslation } from '../../hooks/useTranslation';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';
import { Timestamp } from '../Primitives/Timestamp';

interface BareMetalInstanceTypeDetailsProps {
  bareMetalInstanceType: BareMetalInstanceType;
}

const formatItems = (items: string[]): string => (items.length > 0 ? items.join(', ') : '—');

const formatLabels = (labels: Record<string, string> | undefined): string =>
  formatItems(Object.entries(labels ?? {}).map(([key, value]) => `${key}=${value}`));

const BareMetalInstanceTypeDetails = ({
  bareMetalInstanceType,
}: BareMetalInstanceTypeDetailsProps) => {
  const { t } = useTranslation();
  const { metadata, spec } = bareMetalInstanceType;
  const hardware = spec?.hardware;

  return (
    <Grid hasGutter>
      <GridItem md={6}>
        <Card isFullHeight>
          <CardTitle>{t('Details')}</CardTitle>
          <CardBody>
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(metadata?.name)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(spec?.description)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Host labels')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatLabels(spec?.hostLabelSelector?.matchLabels)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <Timestamp value={metadata?.creationTimestamp} />
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Creator')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(metadata?.creator)}
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
      </GridItem>
      <GridItem md={6}>
        <Card isFullHeight>
          <CardTitle>{t('CPU & Memory')}</CardTitle>
          <CardBody>
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Cores')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {hardware?.cpu?.cores ?? '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Architecture')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(hardware?.cpu?.architecture)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Model')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(hardware?.cpu?.model)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Threads per core')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {hardware?.cpu?.threadsPerCore ?? '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Memory')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {hardware?.memory
                    ? `${hardware.memory.totalGb} GB · ${displayValue(hardware.memory.type)}`
                    : '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
      </GridItem>
      <GridItem md={6}>
        <Card isFullHeight>
          <CardTitle>{t('Hardware')}</CardTitle>
          <CardBody>
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Accelerators')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatItems(
                    (hardware?.accelerators ?? []).map((accelerator) =>
                      [
                        accelerator.type,
                        accelerator.model,
                        accelerator.vendor,
                        accelerator.memoryGb === undefined
                          ? undefined
                          : `${accelerator.memoryGb} GB`,
                      ]
                        .filter(Boolean)
                        .join(' · '),
                    ),
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Disks')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatItems(
                    (hardware?.disks ?? []).map(
                      (disk) => `${disk.type} · ${disk.capacityGb} GB · ${disk.interface}`,
                    ),
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Network ports')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatItems(
                    (hardware?.networkPorts ?? []).map((port) =>
                      [port.name, port.role, port.type, port.speed].filter(Boolean).join(' · '),
                    ),
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Capabilities')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatLabels(hardware?.capabilities)}
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
      </GridItem>
    </Grid>
  );
};

const AdminBareMetalInstanceTypeDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const {
    data: bareMetalInstanceType,
    isLoading,
    error,
  } = useGetResource(BareMetalInstanceTypes, { id });
  const name = bareMetalInstanceType?.object?.metadata?.name ?? id;

  return (
    <>
      {deleteOpen && bareMetalInstanceType?.object && (
        <BareMetalInstanceTypeDeleteModal
          instanceType={bareMetalInstanceType?.object}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate(BAREMETAL_INSTANCE_TYPES_LIST_ROUTE)}
        />
      )}

      <ListPage
        title={name}
        description={bareMetalInstanceType?.object?.spec?.description}
        actions={
          bareMetalInstanceType && (
            <ActionList>
              <ActionListGroup>
                <ActionListItem>
                  <Button
                    variant="secondary"
                    onClick={() => navigate(`${BAREMETAL_INSTANCE_TYPES_LIST_ROUTE}/${id}/edit`)}
                  >
                    {t('Edit')}
                  </Button>
                </ActionListItem>
                <ActionListItem>
                  <DeleteResourceButton onClick={() => setDeleteOpen(true)} />
                </ActionListItem>
              </ActionListGroup>
            </ActionList>
          )
        }
        breadcrumb={
          <Breadcrumb>
            <BreadcrumbItem>
              <Button
                variant="link"
                isInline
                onClick={() => navigate(BAREMETAL_INSTANCE_TYPES_LIST_ROUTE)}
              >
                {t('Bare metal instance types')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{name}</BreadcrumbItem>
          </Breadcrumb>
        }
      >
        <ListPageBody isLoading={isLoading} error={error}>
          {bareMetalInstanceType?.object && (
            <BareMetalInstanceTypeDetails bareMetalInstanceType={bareMetalInstanceType.object} />
          )}
        </ListPageBody>
      </ListPage>
    </>
  );
};

export default AdminBareMetalInstanceTypeDetailPage;
