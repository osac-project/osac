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

import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';

import { type BareMetalInstanceWizardValues, hasBareMetalAuthentication } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { NetworkAttachmentReviewFields } from '../../ProvisioningWizard/NetworkAttachmentReviewFields';
import { formatReviewScalar } from '../../ProvisioningWizard/reviewFormatters';

interface Props {
  catalogItem: CatalogItem | null;
}

export const BareMetalReviewStep = ({ catalogItem }: Props) => {
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

  const networking = values.spec.networking;
  const networkAttachment = networking.networkAttachment.value;
  const isCustomNetwork = !networkAttachment.useDefaults;

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
              {catalogItem?.title || catalogItem?.metadata?.name || '—'}
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

          {isCustomNetwork && networkAttachment.attachments.length > 0 && (
            <NetworkAttachmentReviewFields
              virtualNetwork={networkAttachment.attachments[0].virtualNetwork}
              subnet={networkAttachment.attachments[0].subnet}
              securityGroups={networkAttachment.attachments[0].securityGroups}
            />
          )}

          <DescriptionListGroup>
            <DescriptionListTerm>{t('External access')}</DescriptionListTerm>
            <DescriptionListDescription>
              {networking.attachExternalIp.value ? t('Enabled') : t('Disabled')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
