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
import { useTranslation } from '../../../../../hooks/useTranslation';
import { formatReviewScalar } from '../../catalogOverlay';
import { NetworkAttachmentReviewFields } from '../NetworkAttachmentReviewFields';

interface Props {
  catalogItem: CatalogItem | null;
}

export const BareMetalReviewStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();
  const isSecretSource = values.spec.userDataSource === 'secret';
  const selectedUserData = isSecretSource ? values.spec.userDataSecret.name : values.spec.userData;
  const hasAuthentication = hasBareMetalAuthentication(
    values.spec.sshKey,
    isSecretSource ? undefined : values.spec.userData,
    isSecretSource ? values.spec.userDataSecret.name : undefined,
  );

  const networking = values.spec.networking;
  const isCustomNetwork = !networking.useDefaults;

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
              {formatReviewScalar(values.spec.sshKey)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.diskImage.name || values.spec.diskImage.id || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.spec.instanceType.name || '—'}
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

          {isCustomNetwork && networking.attachments.length > 0 && (
            <NetworkAttachmentReviewFields
              virtualNetwork={networking.attachments[0].virtualNetwork}
              subnet={networking.attachments[0].subnet}
              securityGroups={networking.attachments[0].securityGroups}
            />
          )}

          <DescriptionListGroup>
            <DescriptionListTerm>{t('External access')}</DescriptionListTerm>
            <DescriptionListDescription>
              {networking.attachExternalIp ? t('Enabled') : t('Disabled')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
