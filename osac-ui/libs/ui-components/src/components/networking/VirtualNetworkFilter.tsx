import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import type { VirtualNetwork } from '@osac/types';

import { virtualNetworkOptionLabel } from './virtual-network-option-label';
import { useTranslation } from '../../hooks/useTranslation';

const ALL_OPTION_VALUE = '__all__';

interface VirtualNetworkFilterProps {
  virtualNetworks: readonly VirtualNetwork[];
  selectedId: string;
  onChange: (virtualNetworkId: string) => void;
}

/** URL-backed `All virtual networks` filter shared by the list page toolbar. */
const VirtualNetworkFilter = ({
  virtualNetworks,
  selectedId,
  onChange,
}: VirtualNetworkFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const selected = virtualNetworks.find((network) => network.id === selectedId);
  const toggleLabel = selected ? virtualNetworkOptionLabel(selected) : t('All virtual networks');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={setIsOpen}
        onSelect={(_event, value) => {
          if (typeof value === 'string') {
            onChange(value === ALL_OPTION_VALUE ? '' : value);
            setIsOpen(false);
          }
        }}
        toggle={(toggleRef) => (
          <MenuToggle
            ref={toggleRef}
            onClick={() => setIsOpen((open) => !open)}
            isExpanded={isOpen}
            aria-label={t('Filter by virtual network')}
          >
            {toggleLabel}
          </MenuToggle>
        )}
        shouldFocusToggleOnSelect
        aria-label={t('Filter by virtual network')}
      >
        <SelectList>
          <SelectOption value={ALL_OPTION_VALUE} isSelected={selectedId === ''}>
            {t('All virtual networks')}
          </SelectOption>
          {virtualNetworks.map((network) => (
            <SelectOption
              key={network.id}
              value={network.id}
              isSelected={network.id === selectedId}
              description={network.metadata?.project === '' ? t('Root') : network.metadata?.project}
            >
              {virtualNetworkOptionLabel(network)}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default VirtualNetworkFilter;
