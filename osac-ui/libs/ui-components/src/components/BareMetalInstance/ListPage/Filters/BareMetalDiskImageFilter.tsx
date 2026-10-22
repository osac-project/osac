import { useMemo, useState } from 'react';
import { Alert, MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { useDiskImages } from '@osac/ui-components/api/v1/disk-image';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { useTranslation } from '../../../../hooks/useTranslation';

const ALL_OPTION_VALUE = '__all__';

interface BareMetalDiskImageFilterProps {
  selected: string | undefined;
  onChange: (diskImageId: string | undefined) => void;
}

const BareMetalDiskImageFilter = ({ selected, onChange }: BareMetalDiskImageFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const { data: diskImages = [], error } = useDiskImages({});

  const sortedDiskImages = useMemo(() => {
    const names = diskImages
      .map((diskImage) => diskImage.metadata?.name?.trim())
      .filter((name): name is string => name !== undefined);
    return [...new Set(names)].sort((left, right) => left.localeCompare(right));
  }, [diskImages]);

  const allLabel = t('All disk images');
  const selectedLabel = selected || allLabel;

  return (
    <Select
      maxMenuHeight="350px"
      isOpen={isOpen}
      selected={selected ?? ALL_OPTION_VALUE}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? undefined : String(value));
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter bare metal by disk image')}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      {error ? (
        <Alert isInline variant="danger" title={t('Unable to load disk images')}>
          {getErrorMessage(error)}
        </Alert>
      ) : (
        <SelectList>
          <SelectOption value={ALL_OPTION_VALUE} isSelected={!selected}>
            {allLabel}
          </SelectOption>

          {sortedDiskImages.map((diskImage) => (
            <SelectOption key={diskImage} value={diskImage} isSelected={selected === diskImage}>
              {diskImage}
            </SelectOption>
          ))}
        </SelectList>
      )}
    </Select>
  );
};

export default BareMetalDiskImageFilter;
