import { FC } from 'react';
import { ToggleGroup, ToggleGroupItem } from '@patternfly/react-core';
import ListIcon from '@patternfly/react-icons/dist/esm/icons/list-icon';
import ThIcon from '@patternfly/react-icons/dist/esm/icons/th-icon';

import { useUserPreferences } from '@osac/ui-components/hooks/use-user-preferences';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const VIEW_TYPE_PREF = 'view-type';

export type ViewType = 'cards' | 'list';

export const DEFAULT_VIEW_TYPE: ViewType = 'cards';

export const isViewType = (value?: string | null): value is ViewType =>
  value === 'cards' || value === 'list';

interface ViewSwitcherProps {
  pageKey: string;
}

export const getViewTypePrefKey = (pageKey: string) => `${pageKey}-${VIEW_TYPE_PREF}`;

const ViewSwitcher: FC<ViewSwitcherProps> = ({ pageKey }) => {
  const { t } = useTranslation();
  const [viewType, setViewType] = useUserPreferences(getViewTypePrefKey(pageKey));
  const currentViewType: ViewType = isViewType(viewType) ? viewType : DEFAULT_VIEW_TYPE;

  return (
    <ToggleGroup aria-label={t('Toggle view type')}>
      <ToggleGroupItem
        icon={<ThIcon />}
        aria-label={t('Card view')}
        buttonId="card-view"
        isSelected={currentViewType === 'cards'}
        onChange={() => setViewType('cards')}
      />
      <ToggleGroupItem
        icon={<ListIcon />}
        aria-label={t('List view')}
        buttonId="list-view"
        isSelected={currentViewType === 'list'}
        onChange={() => setViewType('list')}
      />
    </ToggleGroup>
  );
};

export default ViewSwitcher;
