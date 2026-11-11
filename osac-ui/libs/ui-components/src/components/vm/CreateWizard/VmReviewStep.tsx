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

import { Projects } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import {
  fullProjectPathToQueryFilter,
  getProjectName,
} from '@osac/ui-components/components/Project/utils';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { ComputeInstanceWizardValues } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { NetworkAttachmentReviewFields } from '../../ProvisioningWizard/NetworkAttachmentReviewFields';
import { getVmStorageRows } from '../../ProvisioningWizard/storageRows';

export const VmReviewStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ComputeInstanceWizardValues>();

  const {
    data: projects,
    isLoading: projectsLoading,
    error: projectsError,
  } = useListResource(Projects, { filter: fullProjectPathToQueryFilter(values.metadata.project) });

  if (projectsLoading) {
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
  const {
    source: userDataSource,
    inline: userDataInline,
    secret: userDataSecret,
  } = values.spec.userData.value;
  const userDataReview = (() => {
    if (userDataSource === 'secret') {
      const name = userDataSecret.trim();
      return name ? name : t('Not configured');
    }
    return userDataInline.trim() ? t('Provided (hidden)') : t('Not configured');
  })();

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
              {projects?.size === 1
                ? getProjectName(projects.items[0], t)
                : values.metadata.project}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(values.metadata.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('SSH public key')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(values.spec.sshKey.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.diskImage.value.name || ''}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.instanceType.value.name || ''}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('User data')}</DescriptionListTerm>
            <DescriptionListDescription>{userDataReview}</DescriptionListDescription>
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
