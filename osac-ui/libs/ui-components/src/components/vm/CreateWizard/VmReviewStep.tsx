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

import { useInstanceType } from '@osac/ui-components/api/v1/instance-types';
import { useProjects } from '@osac/ui-components/api/v1/project';
import {
  fullProjectPathToQueryFilter,
  getProjectName,
} from '@osac/ui-components/components/Project/utils';
import { formatInstanceTypeReviewLabelFromType } from '@osac/ui-components/components/vm/utils';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { ComputeInstanceWizardValues } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { NetworkAttachmentReviewFields } from '../../ProvisioningWizard/NetworkAttachmentReviewFields';
import { formatReviewScalar } from '../../ProvisioningWizard/reviewFormatters';
import { getVmStorageRows } from '../../ProvisioningWizard/storageRows';

export const VmReviewStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ComputeInstanceWizardValues>();

  const {
    data: instanceType,
    isLoading: instanceLoading,
    error: instanceErr,
  } = useInstanceType(values.spec.instanceType.value);

  const {
    data: projects,
    isLoading: projectsLoading,
    error: projectsError,
  } = useProjects({ filter: fullProjectPathToQueryFilter(values.metadata.project) });

  if (instanceLoading || projectsLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  const storageRows = getVmStorageRows(
    t,
    {
      sizeGib: values.spec.bootDisk.sizeGib.value,
      storageTier: values.spec.bootDisk.storageTier.value,
    },
    values.spec.additionalDisks.value,
  );

  // Read networking names directly from formik values (ResourceSelectValue stores name).
  const networking = values.spec.networking;
  const networkAttachment = networking.networkAttachment.value;
  const isCustomNetwork = !networkAttachment.useDefaultNetwork;

  return (
    <Stack hasGutter>
      {!!instanceErr && (
        <StackItem>
          <Alert variant="warning" isInline title={t('Failed to fetch instance type')}>
            {getErrorMessage(instanceErr)}
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
        <Title headingLevel="h3">{t('Configuration')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Configuration')}>
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
              {formatReviewScalar(values.spec.sshKey.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.diskImage.value.name || values.spec.diskImage.value.id || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatInstanceTypeReviewLabelFromType(
                instanceType,
                undefined,
                values.spec.instanceType.value,
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Cloud-init')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(
                values.spec.userDataSource.value === 'secret'
                  ? values.spec.userDataSecret.value
                  : values.spec.userData.value,
                values.spec.userDataSource.value === 'inline',
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Network')}</DescriptionListTerm>
            <DescriptionListDescription>
              {isCustomNetwork ? t('Custom') : t('Tenant default')}
            </DescriptionListDescription>
          </DescriptionListGroup>

          {isCustomNetwork && (
            <NetworkAttachmentReviewFields
              virtualNetwork={networkAttachment.virtualNetwork}
              subnet={networkAttachment.subnet}
              securityGroups={networkAttachment.securityGroups}
            />
          )}

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Auto attach external IP')}</DescriptionListTerm>
            <DescriptionListDescription>
              {networking.autoExternalIpAttachment.value ? t('Yes') : t('No')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3">{t('Storage')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Storage')}>
          {storageRows.map((row) => (
            <DescriptionListGroup key={row.name}>
              <DescriptionListTerm>{row.name}</DescriptionListTerm>
              <DescriptionListDescription>
                {row.size}, {row.storageTier}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ))}
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
