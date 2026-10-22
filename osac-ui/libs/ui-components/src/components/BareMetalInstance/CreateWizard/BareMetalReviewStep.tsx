import {
  Alert,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { type BareMetalInstanceWizardValues, hasBareMetalAuthentication } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { NetworkAttachmentReviewFields } from '../../ProvisioningWizard/NetworkAttachmentReviewFields';
import { formatReviewScalar } from '../../ProvisioningWizard/reviewFormatters';

export const BareMetalReviewStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();
  const isSecretSource = values.spec.userDataSource.value === 'secret';
  const selectedUserData = isSecretSource
    ? values.spec.userDataSecret.value
    : values.spec.userData.value;
  const hasAuthentication = hasBareMetalAuthentication(
    values.spec.sshKey.value,
    isSecretSource ? undefined : values.spec.userData.value,
    isSecretSource ? values.spec.userDataSecret.value : undefined,
  );

  const networkAttachment = values.spec.networkAttachment.value;
  const isCustomNetwork = !networkAttachment.useDefaultNetwork;

  return (
    <Stack hasGutter>
      {!hasAuthentication && (
        <StackItem>
          <Alert
            variant="danger"
            isInline
            title={t(
              'Provide either an SSH public key or user data containing access credentials.',
            )}
          />
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
              {values.metadata.project || t('Default')}
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
              {values.spec.instanceType.value || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('User data')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(selectedUserData, !isSecretSource)}
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
            <DescriptionListTerm>{t('External access')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.autoExternalIpAttachment.value ? t('Enabled') : t('Disabled')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
