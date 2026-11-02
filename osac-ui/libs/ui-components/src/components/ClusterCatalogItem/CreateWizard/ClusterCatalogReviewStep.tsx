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
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { formatClusterNodeSetReview } from '../../Cluster/CreateWizard/clusterNodeSetReview';

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
              {displayValue(values.metadata.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          {role === 'admin' ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Scope')}</DescriptionListTerm>
              <DescriptionListDescription>
                {values.scope === 'global'
                  ? t('Global')
                  : displayValue(values.metadata.tenant.name || '')}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : values.metadata.project.trim() ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
              <DescriptionListDescription>
                {displayValue(values.metadata.project)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : null}
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Provisioning template')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(values.template.name || '')}
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
              {displayValue(values.version.value.name)}
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
              <DescriptionListTerm>{displayValue(nodeSet.name)}</DescriptionListTerm>
              <DescriptionListDescription>
                {formatClusterNodeSetReview(nodeSet.hostType, nodeSet.size)}
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
              {displayValue(values.podCidr.value)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(values.serviceCidr.value)}
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
