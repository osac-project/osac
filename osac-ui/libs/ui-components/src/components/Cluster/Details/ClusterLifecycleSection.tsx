import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import HistoryIcon from '@patternfly/react-icons/dist/esm/icons/history-icon';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { Timestamp } from '../../Primitives/Timestamp';

const ClusterLifecycleSection = ({ cluster }: { cluster: Cluster }) => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg" className="cluster-details__section-title">
          <HistoryIcon aria-hidden />
          {t('Lifecycle')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact aria-label={t('Cluster lifecycle')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={cluster.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Creator')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.metadata?.creator)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ClusterLifecycleSection;
