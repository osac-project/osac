import {
  Alert,
  Bullseye,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Spinner,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { type HostType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  useClusterVersions,
} from '@osac/ui-components/api/v1/cluster-versions';
import { useHostTypes } from '@osac/ui-components/api/v1/host-types';
import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  formatResourceIdForReview,
  formatResourceIdsForReview,
  useSecurityGroups,
  useSubnets,
  useVirtualNetworks,
} from '@osac/ui-components/api/v1/networking';
import { useProjects } from '@osac/ui-components/api/v1/project';
import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';
import {
  fullProjectPathToQueryFilter,
  getProjectName,
} from '@osac/ui-components/components/Project/utils';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { ClusterWizardValues } from './fields';
import { findVersionByName, versionDisplayName } from './versionUtils';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { formatReviewScalar } from '../../catalogOverlay';

const formatNodeSetsForReview = (
  hostTypes: HostType[],
  nodeSetRows: ClusterWizardValues['spec']['nodeSetRows'],
): string => {
  if (nodeSetRows.length === 0) {
    return '—';
  }
  return nodeSetRows
    .map((row) => {
      const hostType = hostTypes.find((h) => h.id === row.hostType);

      return `${hostType?.title || hostType?.metadata?.name || row.hostType}: ${row.size}`;
    })
    .join(', ');
};

interface Props {
  catalogItem: CatalogItem | null;
}

export const ClusterReviewStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ClusterWizardValues>();

  const {
    data = [],
    isLoading,
    error,
  } = useHostTypes({
    filter: cel<HostType>((filter) =>
      filter.field('id').isIn(values.spec.nodeSetRows.map(({ hostType }) => hostType)),
    ),
  });

  const { data: versions = [] } = useClusterVersions({
    filter: CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  });

  const {
    data: projects,
    isLoading: projectsLoading,
    error: projectsError,
  } = useProjects({ filter: fullProjectPathToQueryFilter(values.metadata.project) });

  const isCustomNetwork =
    !values.spec.useDefaultNetwork && Boolean(values.spec.networkAttachment.subnet.trim());

  const { data: virtualNetworks = [] } = useVirtualNetworks(
    { filter: VIRTUAL_NETWORK_READY_LIST_FILTER },
    { enabled: isCustomNetwork },
  );
  const { data: allSubnets = [] } = useSubnets({}, { enabled: isCustomNetwork });
  const { data: allSecurityGroups = [] } = useSecurityGroups({}, { enabled: isCustomNetwork });

  const versionDisplay = versionDisplayName(
    findVersionByName(versions, values.spec.versionName),
    values.spec.versionName,
  );

  if (isLoading || projectsLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  return (
    <Stack hasGutter>
      {!!error && (
        <StackItem>
          <Alert variant="warning" isInline title={t('Failed to fetch host types')}>
            {getErrorMessage(error)}
          </Alert>
        </StackItem>
      )}
      {!!projectsError && (
        <StackItem>
          <Alert variant="warning" isInline title={t('Failed to fetch project')}>
            {getErrorMessage(projectsError)}
          </Alert>
        </StackItem>
      )}
      <StackItem>
        <DescriptionList
          isHorizontal
          isCompact
          aria-label={t('catalogProvision.steps.review.title')}
        >
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {catalogItem?.title || catalogItem?.metadata?.name || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {projects?.length === 1 ? getProjectName(projects[0], t) : values.metadata.project}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.metadata.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('SSH public key')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.sshPublicKey)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Pull secret')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.pullSecretSecret.name}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Version')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(versionDisplay)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Node sets')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatNodeSetsForReview(data, values.spec.nodeSetRows)}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Network')}</DescriptionListTerm>
            <DescriptionListDescription>
              {isCustomNetwork ? t('Custom') : t('Tenant default')}
            </DescriptionListDescription>
          </DescriptionListGroup>

          {isCustomNetwork && (
            <>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Virtual network')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatResourceIdForReview(
                    values.spec.networkAttachment.virtualNetwork,
                    virtualNetworks,
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Subnet')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatResourceIdForReview(values.spec.networkAttachment.subnet, allSubnets)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatResourceIdsForReview(
                    values.spec.networkAttachment.securityGroups,
                    allSecurityGroups,
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
            </>
          )}

          {values.spec.autoExternalIpAttachment && (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Auto External IP')}</DescriptionListTerm>
              <DescriptionListDescription>{t('Enabled')}</DescriptionListDescription>
            </DescriptionListGroup>
          )}

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Pod CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.network.podCidr)}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.network.serviceCidr)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
