import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { ClusterNodeSetMapPolicy } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import CatalogFieldEditabilityLabel from '../CatalogFieldEditabilityLabel';
import { type FieldPolicyLike, catalogFieldPolicyBehavior } from '../catalogFieldPolicyDisplay';
import type { CatalogItemResourceLookups } from '../catalogItemResourceLookups';
import {
  clusterNodeSetItemsFromPolicy,
  findHostTypeForReference,
  formatClusterCatalogHostTypeLabel,
} from '../clusterCatalogItemResourceDisplay';

import './CatalogNodeSets.css';

interface CatalogNodeSetsProps {
  policy: ClusterNodeSetMapPolicy | undefined;
  resourceLookups: CatalogItemResourceLookups;
}

const CatalogNodeSets = ({ policy, resourceLookups }: CatalogNodeSetsProps) => {
  const { t } = useTranslation();
  const behavior = catalogFieldPolicyBehavior(policy as FieldPolicyLike);
  const nodeSets = clusterNodeSetItemsFromPolicy(policy);

  if (!behavior) {
    return null;
  }

  return (
    <DescriptionListGroup>
      <DescriptionListTerm>{t('Node sets')}</DescriptionListTerm>
      <DescriptionListDescription>
        <Stack hasGutter>
          <StackItem>
            <CatalogFieldEditabilityLabel behavior={behavior} />
          </StackItem>
          {nodeSets && Object.keys(nodeSets).length > 0 ? (
            <StackItem>
              <DescriptionList
                isCompact
                aria-label={t('Node sets')}
                className="catalog-node-sets__list"
              >
                {Object.entries(nodeSets).map(([name, nodeSet]) => {
                  const hostType = findHostTypeForReference(
                    resourceLookups.bareMetalInstanceTypes,
                    nodeSet.baremetalInstanceType,
                  );
                  const hostTypeLabel = formatClusterCatalogHostTypeLabel(
                    hostType,
                    nodeSet.baremetalInstanceType,
                  );

                  return (
                    <DescriptionListGroup key={name}>
                      <DescriptionListTerm>{name}</DescriptionListTerm>
                      <DescriptionListDescription>
                        {t(
                          nodeSet.size === 1
                            ? '{{hostType}} · {{size}} node'
                            : '{{hostType}} · {{size}} nodes',
                          {
                            hostType: hostTypeLabel,
                            size: nodeSet.size,
                          },
                        )}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  );
                })}
              </DescriptionList>
            </StackItem>
          ) : (
            <StackItem>{t('Not set')}</StackItem>
          )}
        </Stack>
      </DescriptionListDescription>
    </DescriptionListGroup>
  );
};

export default CatalogNodeSets;
