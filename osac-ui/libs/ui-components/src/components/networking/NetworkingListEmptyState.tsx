import type { ReactNode } from 'react';
import {
  Button,
  EmptyState,
  EmptyStateActions,
  EmptyStateBody,
  EmptyStateFooter,
} from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';

interface NetworkingListEmptyStateProps {
  /** Whether filters are set; then the no-results state with Clear all filters is shown. */
  isFiltered?: boolean;
  /** Short title when the list has no resources at all, such as "No subnets". */
  emptyTitle: string;
  /** What the list holds or what to do next. */
  emptyBody: ReactNode;
  /** The create action of the list. */
  emptyAction?: ReactNode;
  onClearFilters?: () => void;
}

/** The empty and no-results states of every networking list: title, body and action. */
const NetworkingListEmptyState = ({
  isFiltered = false,
  emptyTitle,
  emptyBody,
  emptyAction,
  onClearFilters,
}: NetworkingListEmptyStateProps) => {
  const { t } = useTranslation();

  if (isFiltered) {
    return (
      <EmptyState titleText={t('No results found')} headingLevel="h2">
        <EmptyStateBody>
          {t('No results match the filter criteria. Clear all filters and try again.')}
        </EmptyStateBody>
        {onClearFilters ? (
          <EmptyStateFooter>
            <EmptyStateActions>
              <Button variant="link" onClick={onClearFilters}>
                {t('Clear all filters')}
              </Button>
            </EmptyStateActions>
          </EmptyStateFooter>
        ) : null}
      </EmptyState>
    );
  }

  return (
    <EmptyState titleText={emptyTitle} headingLevel="h2">
      <EmptyStateBody>{emptyBody}</EmptyStateBody>
      {emptyAction ? (
        <EmptyStateFooter>
          <EmptyStateActions>{emptyAction}</EmptyStateActions>
        </EmptyStateFooter>
      ) : null}
    </EmptyState>
  );
};

export default NetworkingListEmptyState;
