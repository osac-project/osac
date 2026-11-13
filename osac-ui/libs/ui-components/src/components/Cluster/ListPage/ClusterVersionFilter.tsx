import { useMemo, useState } from 'react';
import { Alert, MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { ClusterVersion, ClusterVersionReference } from '@osac/types';
import { formatClusterCatalogVersionLabel } from '@osac/ui-components/components/catalog/clusterCatalogItemResourceDisplay';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { useTranslation } from '../../../hooks/useTranslation';

import './ClusterVersionFilter.css';

interface ClusterVersionFilterProps {
  clusterVersions: ClusterVersion[];
  selected: readonly string[];
  onClear: () => void;
  onToggle: (value: string) => void;
  loadError?: unknown;
}

const ClusterVersionFilter = ({
  clusterVersions = [],
  selected,
  onClear,
  onToggle,
  loadError,
}: ClusterVersionFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const versionFilterOptions = useMemo(() => {
    const options = clusterVersions.reduce<{ value: string; label: string }[]>((acc, version) => {
      const name = version.metadata?.name?.trim();
      if (!name) {
        return acc;
      }

      const reference = {
        id: version.id,
        name,
        project: version.metadata?.project,
      } as ClusterVersionReference;

      const label = formatClusterCatalogVersionLabel(version, reference);
      if (label && label !== '—') {
        acc.push({ value: name, label });
      }
      return acc;
    }, []);
    return options.sort((a, b) => a.label.localeCompare(b.label));
  }, [clusterVersions]);

  const invalidSelections = useMemo(() => {
    if (!selected?.length) {
      return [];
    }
    return selected.filter(
      (version) => !clusterVersions.find((cv) => cv.metadata?.name?.trim() === version),
    );
  }, [clusterVersions, selected]);

  const labelByValue = useMemo(
    () => new Map(versionFilterOptions.map((option) => [option.value, option.label])),
    [versionFilterOptions],
  );

  const allLabel = t('All cluster versions');
  const toggleLabel = selected.length
    ? selected.length === 1
      ? (labelByValue.get(selected[0]) ?? selected[0])
      : t('{{count}} cluster versions', { count: selected.length })
    : allLabel;

  return (
    <Select
      role="menu"
      maxMenuHeight="300px"
      isOpen={isOpen}
      onSelect={(_, value) => {
        if (value === null) {
          onClear();
          setIsOpen(false);
          return;
        }
        if (typeof value === 'string') {
          onToggle(value);
        }
      }}
      onOpenChange={setIsOpen}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter by cluster version')}
        >
          {toggleLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {loadError ? (
          <Alert
            variant="danger"
            isInline
            title={t('Could not load cluster version filter options')}
          >
            {getErrorMessage(loadError)}
          </Alert>
        ) : (
          <>
            <SelectOption value={null}>{allLabel}</SelectOption>
            {versionFilterOptions.map((option) => (
              <SelectOption
                key={option.value}
                value={option.value}
                hasCheckbox
                isSelected={selected.includes(option.value)}
              >
                {option.label}
              </SelectOption>
            ))}
            {invalidSelections.map((option) => (
              <SelectOption
                className="cluster-version-filter--invalid-option"
                key={option}
                value={option}
                hasCheckbox
                isSelected
              >
                {option}
              </SelectOption>
            ))}
          </>
        )}
      </SelectList>
    </Select>
  );
};

export default ClusterVersionFilter;
