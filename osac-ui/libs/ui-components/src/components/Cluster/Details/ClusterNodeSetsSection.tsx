import {
  Content,
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
import { displayValue } from '../../../utils/detailFormatters';

interface ClusterNodeSetsSectionProps {
  cluster: Cluster;
}

const ClusterNodeSetsSection = ({ cluster }: ClusterNodeSetsSectionProps) => {
  const { t } = useTranslation();

  const nodeSetEntries = Object.entries(cluster.spec?.nodeSets ?? {});

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h3" size="md">
          {t('Node sets')}
        </Title>
      </StackItem>
      <StackItem>
        {nodeSetEntries.length > 0 ? (
          <ul
            aria-label={t('Cluster node sets')}
            className="cluster-details__node-sets"
            role="list"
          >
            {nodeSetEntries.map(([name, nodeSet]) => (
              <li key={name}>
                <DescriptionList isCompact className="cluster-details__node-set-row">
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
                    <DescriptionListDescription>{name}</DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Host type')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {displayValue(nodeSet.baremetalInstanceType?.name)}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Size')}</DescriptionListTerm>
                    <DescriptionListDescription>{nodeSet.size}</DescriptionListDescription>
                  </DescriptionListGroup>
                </DescriptionList>
              </li>
            ))}
          </ul>
        ) : (
          <Content component="p">{t('No node sets configured.')}</Content>
        )}
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsSection;
