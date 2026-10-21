import { Button, Content, Flex, FlexItem, Stack, StackItem } from '@patternfly/react-core';

import FieldSeparator from '@osac/ui-components/components/Primitives/FieldSeparator';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

type ListPageToolbar = {
  filteredText?: string;
  isFiltered?: boolean;
  onClearAllFilters?: () => void;
};

const ListPageToolbar = ({
  filteredText,
  isFiltered,
  onClearAllFilters,
  children,
}: React.PropsWithChildren<ListPageToolbar>) => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>{children}</StackItem>
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
      </>
    </Stack>
  );
};

export default ListPageToolbar;
