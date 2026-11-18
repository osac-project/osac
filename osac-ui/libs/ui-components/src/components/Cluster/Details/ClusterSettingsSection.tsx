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
            <DescriptionListTerm className="pf-v6-u-text-color-subtle pf-v6-u-font-weight-normal">
              {t('SSH public key')}
            </DescriptionListTerm>
            <DescriptionListDescription
              className="cluster-details__ssh-key pf-v6-u-font-size-sm"
              tabIndex={cluster.spec?.sshPublicKey?.trim() ? 0 : undefined}
            >
              {displayValue(cluster.spec?.sshPublicKey)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm className="pf-v6-u-text-color-subtle pf-v6-u-font-weight-normal">
              {t('Pod CIDR')}
            </DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.spec?.network?.podCidr)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm className="pf-v6-u-text-color-subtle pf-v6-u-font-weight-normal">
              {t('Service CIDR')}
            </DescriptionListTerm>
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
