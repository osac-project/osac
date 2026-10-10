import { ReactNode } from 'react';
import { Button, Content, Flex, FlexItem, Stack, StackItem } from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ListPage from './ListPage';
import ListPageBody from './ListPageBody';
import FieldSeparator from '../Primitives/FieldSeparator';

export interface PageViewPageBodyProps {
  isLoading: boolean;
  error?: unknown;
}

export interface PageViewProps {
  label?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
  error?: unknown;
  toolbar: ReactNode;
  showEmptyState: boolean;
  emptyState: ReactNode;
  listSection: ReactNode;
  filteredText?: string;
  isFiltered?: boolean;
  onClearAllFilters?: () => void;
  pageBody?: PageViewPageBodyProps;
}

const PageView = ({
  label,
  title,
  description,
  actions,
  breadcrumb,
  error,
  toolbar,
  showEmptyState,
  emptyState,
  listSection,
  filteredText,
  isFiltered = false,
  onClearAllFilters,
  pageBody,
}: PageViewProps) => {
  const { t } = useTranslation();

  const content = (
    <Stack hasGutter>
      <StackItem>{toolbar}</StackItem>
      {showEmptyState ? (
        <StackItem>{emptyState}</StackItem>
      ) : (
        <>
          {filteredText ? (
            <StackItem>
              <Flex gap={{ default: 'gapXs' }}>
                <FlexItem>
                  <Content className="pf-v6-u-font-weight-bold">{filteredText}</Content>
                </FlexItem>
                {isFiltered && onClearAllFilters ? (
                  <>
                    <FlexItem>
                      <FieldSeparator />
                    </FlexItem>
                    <FlexItem>
                      <Button variant="link" isInline onClick={onClearAllFilters}>
                        {t('Clear all filters')}
                      </Button>
                    </FlexItem>
                  </>
                ) : null}
              </Flex>
            </StackItem>
          ) : null}
          <StackItem>{listSection}</StackItem>
        </>
      )}
    </Stack>
  );

  return (
    <ListPage
      label={label}
      title={title}
      description={description}
      actions={actions}
      breadcrumb={breadcrumb}
      error={error}
    >
      {pageBody ? (
        <ListPageBody isLoading={pageBody.isLoading} error={pageBody.error ?? error}>
          {content}
        </ListPageBody>
      ) : (
        content
      )}
    </ListPage>
  );
};

export default PageView;
