import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { Timestamp } from '../../Primitives/Timestamp';

const ClusterLifecycleSection = ({ cluster }: { cluster: Cluster }) => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter className="pf-v6-u-min-width">
      <StackItem>
        <Title headingLevel="h2" size="lg">
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
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ClusterLifecycleSection;
