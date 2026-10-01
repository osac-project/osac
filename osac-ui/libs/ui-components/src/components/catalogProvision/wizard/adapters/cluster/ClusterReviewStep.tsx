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
  Title,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { type HostType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  useClusterVersions,
} from '@osac/ui-components/api/v1/cluster-versions';
import { useHostTypes } from '@osac/ui-components/api/v1/host-types';
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
import { NetworkAttachmentReviewFields } from '../NetworkAttachmentReviewFields';

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

  const isCustomNetwork = !values.spec.useDefaultNetwork;

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
        </DescriptionList>
      </StackItem>

      <StackItem>
        <Title headingLevel="h3">{t('Infrastructure Networking')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Network')}</DescriptionListTerm>
            <DescriptionListDescription>
              {isCustomNetwork ? t('Custom') : t('Tenant default')}
            </DescriptionListDescription>
          </DescriptionListGroup>

          {isCustomNetwork && (
            <NetworkAttachmentReviewFields
              virtualNetwork={values.spec.networkAttachment.virtualNetwork}
              subnet={values.spec.networkAttachment.subnet}
              securityGroups={values.spec.networkAttachment.securityGroups}
            />
          )}

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Auto attach external IP')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.autoExternalIpAttachment ? t('Yes') : t('No')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>

      <StackItem>
        <Title headingLevel="h3">{t('Cluster Networking')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact>
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
