import { Content, Divider, List, ListItem, Stack, StackItem, Title } from '@patternfly/react-core';

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
        <Divider />
      </StackItem>
      <StackItem>
        {nodeSetEntries.length > 0 ? (
          <List
            isPlain
            aria-label={t('Cluster node sets')}
            role="list"
            className="pf-v6-u-text-break-word"
          >
            {nodeSetEntries.map(([name, nodeSet]) => (
              <ListItem key={name}>
                <strong className="pf-v6-u-display-block">{name}</strong>
                <Content component="p" className="pf-v6-u-text-color-subtle pf-v6-u-font-size-sm">
                  {t('{{hostType}}-{{count}} Nodes-{{version}}', {
                    replace: {
                      hostType: displayValue(nodeSet.baremetalInstanceType?.name),
                      count: nodeSet.size ?? '—',
                      version: displayValue(cluster.status?.nodeSets[name]?.observedVersion),
                    },
                  })}
                </Content>
              </ListItem>
            ))}
          </List>
        ) : (
          <Content component="p">{t('No node sets configured.')}</Content>
        )}
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsSection;
