import { FC } from 'react';
import { ToggleGroup, ToggleGroupItem } from '@patternfly/react-core';
import ListIcon from '@patternfly/react-icons/dist/esm/icons/list-icon';
import ThIcon from '@patternfly/react-icons/dist/esm/icons/th-icon';

import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

interface ViewSwitcherProps {
  pageKey: string;
}

const ViewSwitcher: FC<ViewSwitcherProps> = ({ pageKey }) => {
  const { t } = useTranslation();
  const [viewType, setViewType] = useViewType(pageKey);

  return (
    <ToggleGroup aria-label={t('Toggle view type')}>
      <ToggleGroupItem
        icon={<ThIcon />}
        aria-label={t('Card view')}
        buttonId="card-view"
        isSelected={viewType === 'cards'}
        onChange={() => setViewType('cards')}
      />
      <ToggleGroupItem
        icon={<ListIcon />}
        aria-label={t('List view')}
        buttonId="list-view"
        isSelected={viewType === 'list'}
        onChange={() => setViewType('list')}
      />
    </ToggleGroup>
  );
};

export default ViewSwitcher;
