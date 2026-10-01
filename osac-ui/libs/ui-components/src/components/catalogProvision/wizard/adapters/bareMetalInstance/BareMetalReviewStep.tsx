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

interface Props {
  catalogItem: CatalogItem | null;
}

export const BareMetalReviewStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();
  const hasAuthentication = hasBareMetalAuthentication(values.spec.sshKey, values.spec.userData);

  // Format networking summary from formik values (ResourceSelectValue stores name).
  const networking = values.spec.networking;
  const networkingSummary = networking.useDefaults
    ? t('Using tenant default network')
    : networking.attachments
        .slice(0, 1)
        .map((attachment) => {
          const vnName = attachment.virtualNetwork.name || attachment.virtualNetwork.id || '—';
          const subnetName = attachment.subnet.name || attachment.subnet.id || '—';
          const sgNames =
            attachment.securityGroups.length > 0
              ? attachment.securityGroups.map((sg) => sg.name || sg.id).join(', ')
              : '—';
          return `${vnName} / ${subnetName} / ${sgNames}`;
        })
        .join('\n');

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
              {formatReviewScalar(values.spec.userData, true)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Networking')}</DescriptionListTerm>
            <DescriptionListDescription style={{ whiteSpace: 'pre-line' }}>
              {networkingSummary}
            </DescriptionListDescription>
          </DescriptionListGroup>
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
