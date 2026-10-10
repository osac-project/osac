import * as React from 'react';
import { Content, Flex, FlexItem, PageSection, Stack, Title } from '@patternfly/react-core';

type ListPageProps = {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  breadcrumb?: React.ReactNode;
  error?: unknown;
  children: React.ReactNode;
};

const ListPage: React.FC<ListPageProps> = ({
  title,
  description,
  actions,
  breadcrumb,
  error,
  children,
}) => (
  <PageSection hasBodyWrapper={false}>
    <Stack>
      <Flex
        gap={{ default: 'gapMd' }}
        alignItems={{ default: breadcrumb ? 'alignItemsFlexStart' : 'alignItemsCenter' }}
        justifyContent={{ default: 'justifyContentSpaceBetween' }}
      >
        <FlexItem>
          <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsSm' }}>
            {breadcrumb ? <FlexItem>{breadcrumb}</FlexItem> : null}
            <FlexItem>
              <Title headingLevel="h1" size="3xl">
                {title}
              </Title>
              {description && <Content component="p">{description}</Content>}
            </FlexItem>
          </Flex>
        </FlexItem>
        {actions && !error ? <FlexItem>{actions}</FlexItem> : null}
      </Flex>
    </Stack>
    {children}
  </PageSection>
);

export default ListPage;
