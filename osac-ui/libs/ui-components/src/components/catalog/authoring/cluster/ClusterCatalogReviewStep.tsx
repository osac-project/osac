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

import type { ClusterCatalogItemValues } from './values';
import { useSession } from '../../../../hooks/use-session';
import { useTranslation } from '../../../../hooks/useTranslation';
import { formatReviewScalar } from '../../../ProvisioningWizard/reviewFormatters';

const ClusterCatalogReviewStep = () => {
  const { t } = useTranslation();
  const { role } = useSession();
  const { values } = useFormikContext<ClusterCatalogItemValues>();

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
          {role === 'admin' ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Scope')}</DescriptionListTerm>
              <DescriptionListDescription>
                {values.scope === 'global'
                  ? t('Global')
                  : formatReviewScalar(values.metadata.tenant.name || values.metadata.tenant.id)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : values.metadata.project.trim() ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
              <DescriptionListDescription>
                {formatReviewScalar(values.metadata.project)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : null}
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
            <DescriptionListTerm>{t('Version')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.version.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3">{t('Node sets')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Node sets')}>
          {values.nodeSets.value.map((nodeSet) => (
            <DescriptionListGroup key={nodeSet.rowId}>
              <DescriptionListTerm>{formatReviewScalar(nodeSet.name)}</DescriptionListTerm>
              <DescriptionListDescription>
                {t('{{hostType}}, {{size}} nodes', {
                  hostType: nodeSet.hostType,
                  size: nodeSet.size,
                })}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ))}
        </DescriptionList>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3">{t('Networking')}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Networking')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Pod CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.podCidr.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.serviceCidr.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
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

export default ClusterCatalogReviewStep;
