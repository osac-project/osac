import {
  Alert,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import InfoCircleIcon from '@patternfly/react-icons/dist/esm/icons/info-circle-icon';
import { useFormikContext } from 'formik';

import type { ComputeCatalogItemValues } from './values';
import { useInstanceType } from '../../../../api/v1/instance-types';
import { useSession } from '../../../../hooks/use-session';
import { useTranslation } from '../../../../hooks/useTranslation';
import { formatReviewScalar } from '../../../ProvisioningWizard/reviewFormatters';
import { getVmStorageRows } from '../../../ProvisioningWizard/storageRows';
import { formatInstanceTypeReviewLabelFromType } from '../../../vm/utils';

const ComputeCatalogReviewStep = () => {
  const { t } = useTranslation();
  const { role } = useSession();
  const { values } = useFormikContext<ComputeCatalogItemValues>();
  const { data: instanceType } = useInstanceType(values.instanceType.value.id);

  const storageRows = getVmStorageRows(
    t,
    { sizeGib: values.bootDiskSizeGib.value, storageTier: values.bootDiskStorageTier.value },
    values.additionalDisks.value,
  );
  const userDataReview =
    values.userDataSource.value === 'secret'
      ? values.userDataSecret.value.trim()
        ? values.userDataSecret.value
        : t('Not set')
      : values.userData.value.trim()
        ? t('Configured')
        : t('Not set');

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Review')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t('Review the configured defaults before creating this catalog item.')}
        </Content>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3">{t('General')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('General')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.metadata.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.metadata.description)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          {role === 'admin' ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Scope')}</DescriptionListTerm>
              <DescriptionListDescription>
                {values.scope === 'global'
                  ? t('Global')
                  : formatReviewScalar(values.metadata.tenant.name || values.metadata.tenant.id)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
              <DescriptionListDescription>
                {formatReviewScalar(values.metadata.project)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          )}
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Provisioning template')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.template.name || values.template.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3">{t('Configuration')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Configuration')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.diskImage.value.name || values.diskImage.value.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatInstanceTypeReviewLabelFromType(
                instanceType,
                undefined,
                values.instanceType.value.id,
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3">{t('Access & Initial run')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Access & Initial run')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Cloud-init')}</DescriptionListTerm>
            <DescriptionListDescription>{userDataReview}</DescriptionListDescription>
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
      <StackItem>
        <Alert
          variant="custom"
          customIcon={<InfoCircleIcon />}
          isInline
          title={t('Starts as unpublished')}
        >
          {role === 'admin'
            ? t(
                'New catalog items are saved as unpublished. Publish from the catalog when you are ready for tenants to use this offering.',
              )
            : t(
                'New catalog items are saved as unpublished. Publish from the catalog when you are ready for users in your tenant to use this offering.',
              )}
        </Alert>
      </StackItem>
    </Stack>
  );
};

export default ComputeCatalogReviewStep;
