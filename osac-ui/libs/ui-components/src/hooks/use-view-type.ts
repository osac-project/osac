import { useUserPreferences } from '@osac/ui-components/hooks/use-user-preferences';

export const VIEW_TYPE_PREF = 'view-type';

export type ViewType = 'cards' | 'list';

export const DEFAULT_VIEW_TYPE: ViewType = 'cards';

export const isViewType = (value?: string | null): value is ViewType =>
  value === 'cards' || value === 'list';

export const getViewTypePrefKey = (pageKey: string) => `${pageKey}-${VIEW_TYPE_PREF}`;

export const useViewType = (
  pageKey: string,
): [viewType: ViewType, setViewType: (viewType: ViewType) => void] => {
  const [viewType, setViewType] = useUserPreferences(getViewTypePrefKey(pageKey));
  const currentViewType: ViewType = isViewType(viewType) ? viewType : DEFAULT_VIEW_TYPE;

  return [currentViewType, setViewType];
};
