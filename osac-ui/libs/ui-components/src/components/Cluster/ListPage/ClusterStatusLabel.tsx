import { ClusterState } from '@osac/types';
import {
  ClusterStatusFilter,
  clusterStatusFilterLabel,
} from '@osac/ui-components/components/Cluster/ListPage/clusterDisplay';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../../Resource/ResourceStatusLabel';

interface ClusterStatusLabelProps {
  state?: ClusterState;
}

export const ClusterStatusLabel = ({ state }: ClusterStatusLabelProps) => {
  const { t } = useTranslation();

  let status: StatusKind = 'unspecified';
  let statusFilter: ClusterStatusFilter | undefined = undefined;

  switch (state) {
    case ClusterState.PROGRESSING:
      status = 'progressing';
      statusFilter = 'progressing';
      break;
    case ClusterState.READY:
      status = 'ready';
      statusFilter = 'ready';
      break;
    case ClusterState.FAILED:
      status = 'failed';
      statusFilter = 'failed';
      break;
    case ClusterState.DELETING:
      status = 'progressing';
      statusFilter = 'deleting';
      break;
    case ClusterState.DELETE_FAILED:
      status = 'failed';
      statusFilter = 'delete-failed';
      break;
    default:
      break;
  }
  const text = statusFilter ? clusterStatusFilterLabel(statusFilter, t) : t('Unknown');

  return <ResourceStatusLabel status={status} text={text} />;
};
