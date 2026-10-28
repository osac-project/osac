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

import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  useClusterVersions,
} from '@osac/ui-components/api/v1/cluster-versions';
import { useProjects } from '@osac/ui-components/api/v1/project';
import {
  fullProjectPathToQueryFilter,
  getProjectName,
} from '@osac/ui-components/components/Project/utils';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { ClusterWizardValues } from './fields';
import { findVersionByName, versionDisplayName } from './versionUtils';
import { useTranslation } from '../../../hooks/useTranslation';
import { NetworkAttachmentReviewFields } from '../../ProvisioningWizard/NetworkAttachmentReviewFields';
import { formatReviewScalar } from '../../ProvisioningWizard/reviewFormatters';

const formatNodeSetsForReview = (
  nodeSetRows: ClusterWizardValues['spec']['nodeSetRows'],
): string => {
  if (nodeSetRows.value.length === 0) {
    return '—';
  }
  return nodeSetRows.value
    .map((row) => {
      return `${row.hostType}: ${row.size}`;
    })
    .join(', ');
};

export const ClusterReviewStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ClusterWizardValues>();

  const { data: versions = [] } = useClusterVersions({
    filter: CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  });

  const {
    data: projects,
    isLoading: projectsLoading,
    error: projectsError,
  } = useProjects({ filter: fullProjectPathToQueryFilter(values.metadata.project) });

  const isCustomNetwork = !values.spec.networkAttachment.value.useDefaultNetwork;

  const versionDisplay = versionDisplayName(
    findVersionByName(versions, values.spec.versionName.value),
    values.spec.versionName.value,
  );

  if (projectsLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  return (
    <Stack hasGutter>
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
              {values.catalogItemReference.name || '—'}
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
              {formatReviewScalar(values.spec.sshPublicKey.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Pull secret')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.pullSecretSecret.value)}
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
              {formatNodeSetsForReview(values.spec.nodeSetRows)}
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
              virtualNetwork={values.spec.networkAttachment.value.virtualNetwork}
              subnet={values.spec.networkAttachment.value.subnet}
              securityGroups={values.spec.networkAttachment.value.securityGroups}
              showSecurityGroups={false}
            />
          )}

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Auto attach external IP')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.autoExternalIpAttachment.value ? t('Yes') : t('No')}
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
              {formatReviewScalar(values.spec.network.podCidr.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.network.serviceCidr.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
