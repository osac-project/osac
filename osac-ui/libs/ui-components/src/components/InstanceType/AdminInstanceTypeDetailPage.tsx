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
  Stack,
  StackItem,
} from '@patternfly/react-core';

import {
  InstanceTypeState,
  InstanceTypes,
  type InstanceType as PrivateInstanceType,
} from '@osac/types/private';
import { useGetResource } from '@osac/ui-components/api/use-resource';

import InstanceTypeDeleteConfirmModal from './InstanceTypeDeleteConfirmModal';
import InstanceTypeLifecycleLabel from './InstanceTypeLifecycleLabel';
import {
  getInstanceTypeLifecycleActions,
  useInstanceTypeLifecycleAction,
} from './useInstanceTypeLifecycleAction';
import { useTranslation } from '../../hooks/useTranslation';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';
import { Timestamp } from '../Primitives/Timestamp';
import { SubtleContent } from '../SubtleContent/SubtleContent';

export const INSTANCE_TYPES_LIST_ROUTE = '/admin/infrastructure/instance-types';

interface InstanceTypeDetailActionsProps {
  instanceType: PrivateInstanceType;
  onDeleted: () => void;
}

const InstanceTypeDetailActions = ({ instanceType, onDeleted }: InstanceTypeDetailActionsProps) => {
  const { t } = useTranslation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { runLifecycleAction } = useInstanceTypeLifecycleAction();

  const { canDeprecate, canObsolete, canReactivate, canDelete } = getInstanceTypeLifecycleActions(
    instanceType.spec?.state,
  );

  return (
    <>
      <ActionList>
        <ActionListGroup>
          {canDeprecate && (
            <ActionListItem>
              <Button
                variant="secondary"
                onClick={() => runLifecycleAction(instanceType.id, InstanceTypeState.DEPRECATED)}
              >
                {t('Deprecate')}
              </Button>
            </ActionListItem>
          )}
          {canObsolete && (
            <ActionListItem>
              <Button
                variant="secondary"
                onClick={() => runLifecycleAction(instanceType.id, InstanceTypeState.OBSOLETE)}
              >
                {t('Obsolete')}
              </Button>
            </ActionListItem>
          )}
          {canReactivate && (
            <ActionListItem>
              <Button
                variant="secondary"
                onClick={() => runLifecycleAction(instanceType.id, InstanceTypeState.ACTIVE)}
              >
                {t('Reactivate')}
              </Button>
            </ActionListItem>
          )}
          {canDelete && (
            <ActionListItem>
              <Button variant="danger" onClick={() => setDeleteOpen(true)}>
                {t('Delete')}
              </Button>
            </ActionListItem>
          )}
        </ActionListGroup>
      </ActionList>

      {deleteOpen && (
        <InstanceTypeDeleteConfirmModal
          instanceType={instanceType}
          onClose={() => setDeleteOpen(false)}
          onSuccess={onDeleted}
        />
      )}
    </>
  );
};

const AdminInstanceTypeDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();

  const { data, isLoading, error } = useGetResource(InstanceTypes, { id });

  const instanceType = data?.object;

  const name = instanceType?.metadata?.name ?? id;
  const gpu = instanceType?.spec?.gpu;

  return (
    <ListPage
      title={name}
      description={instanceType?.spec?.description}
      actions={
        instanceType && (
          <InstanceTypeDetailActions
            instanceType={instanceType}
            onDeleted={() => navigate(INSTANCE_TYPES_LIST_ROUTE)}
          />
        )
      }
      breadcrumb={
        <Breadcrumb>
          <BreadcrumbItem>
            <Button variant="link" isInline onClick={() => navigate(INSTANCE_TYPES_LIST_ROUTE)}>
              {t('Instance types')}
            </Button>
          </BreadcrumbItem>
          <BreadcrumbItem isActive>{name}</BreadcrumbItem>
        </Breadcrumb>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Stack hasGutter>
          <StackItem>
            <Card>
              <CardTitle>{t('Details')}</CardTitle>
              <CardBody>
                <DescriptionList isCompact columnModifier={{ default: '2Col' }}>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      <code>{instanceType?.metadata?.name ?? id}</code>
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('vCPUs')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {instanceType?.spec?.vcpus ?? '—'}
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Lifecycle state')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      <InstanceTypeLifecycleLabel state={instanceType?.spec?.state} />
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Memory (GiB)')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {instanceType?.spec?.memoryGib ?? '—'}
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      <Timestamp value={instanceType?.metadata?.creationTimestamp} />
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                </DescriptionList>
              </CardBody>
            </Card>
          </StackItem>

          <StackItem>
            <Card>
              <CardTitle>{t('GPU')}</CardTitle>
              <CardBody>
                {gpu ? (
                  <DescriptionList isCompact isFillColumns columnModifier={{ default: '2Col' }}>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Count')}</DescriptionListTerm>
                      <DescriptionListDescription>{gpu.count}</DescriptionListDescription>
                    </DescriptionListGroup>

                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Resource name')}</DescriptionListTerm>
                      <DescriptionListDescription>{gpu.resourceName}</DescriptionListDescription>
                    </DescriptionListGroup>

                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('PCI device selector')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        <code>{gpu.pciDeviceSelector}</code>
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  </DescriptionList>
                ) : (
                  <SubtleContent component="p">
                    {t('This instance type has no GPU attached.')}
                  </SubtleContent>
                )}
              </CardBody>
            </Card>
          </StackItem>
        </Stack>
      </ListPageBody>
    </ListPage>
  );
};

export default AdminInstanceTypeDetailPage;
