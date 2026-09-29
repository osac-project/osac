import { useMemo } from 'react';
import { Flex, FlexItem, Label, ToggleGroup, ToggleGroupItem } from '@patternfly/react-core';

import { ServiceTier } from '@osac/types';
import { useSession } from '@osac/ui-components/hooks/use-session';

import { useTranslation } from '../../hooks/useTranslation';

interface CatalogServiceTierFilterProps {
  selectedTypeOptions: ServiceTier[];
  typeCounts: Record<ServiceTier, number>;
  toggleTypeFilter: (value: ServiceTier) => void;
}

const CatalogServiceTierFilter = ({
  selectedTypeOptions,
  typeCounts,
  toggleTypeFilter,
}: CatalogServiceTierFilterProps) => {
  const { t } = useTranslation();
  const { enabledServices } = useSession();

  const catalogTypeFilters = useMemo<ReadonlyArray<{ value: ServiceTier; label: string }>>(
    () => [
      ...(enabledServices.includes(ServiceTier.BMAAS)
        ? [{ value: ServiceTier.BMAAS, label: t('Bare Metal Machines') }]
        : []),
      ...(enabledServices.includes(ServiceTier.CAAS)
        ? [{ value: ServiceTier.CAAS, label: t('Clusters') }]
        : []),
      ...(enabledServices.includes(ServiceTier.VMAAS)
        ? [{ value: ServiceTier.VMAAS, label: t('Virtual Machines') }]
        : []),
    ],
    [t, enabledServices],
  );

  return (
    <ToggleGroup aria-label={t('Filter catalog by resource type')}>
      {catalogTypeFilters
        .filter(({ value }) => enabledServices.includes(value))
        .map((option) => {
          const count = typeCounts[option.value];

          return (
            <ToggleGroupItem
              key={option.value}
              text={
                <Flex spaceItems={{ default: 'spaceItemsSm' }} flexWrap={{ default: 'nowrap' }}>
                  <FlexItem>{option.label}</FlexItem>
                  <FlexItem>
                    <Label isCompact>{count}</Label>
                  </FlexItem>
                </Flex>
              }
              buttonId={`catalog-type-filter-${option.value}`}
              isSelected={selectedTypeOptions.includes(option.value)}
              onChange={() => {
                toggleTypeFilter(option.value);
              }}
            />
          );
        })}
    </ToggleGroup>
  );
};

export default CatalogServiceTierFilter;
