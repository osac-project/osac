import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import ResourceLink from '../../Resource/ResourceLink';

const ClusterSettingsSection = ({ cluster }: { cluster: Cluster }) => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h3" size="md">
          {t('Settings')}
        </Title>
        <Divider />
      </StackItem>
      <StackItem>
        <DescriptionList
          isCompact
          aria-label={t('Cluster settings')}
          className="pf-v6-u-text-break-word"
        >
          <DescriptionListGroup>
            <DescriptionListTerm>{t('SSH public key')}</DescriptionListTerm>
            <DescriptionListDescription className="cluster-details__ssh-key">
              {cluster.spec?.sshKey?.id ? (
                <ResourceLink
                  basePath="/secrets"
                  id={cluster.spec.sshKey.id}
                  name={cluster.spec.sshKey.name || cluster.spec.sshKey.id}
                />
              ) : (
                displayValue(cluster.spec?.sshKey?.name)
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Pod CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.spec?.network?.podCidr)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.spec?.network?.serviceCidr)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ClusterSettingsSection;
