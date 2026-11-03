import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Content,
  Flex,
  FlexItem,
  Skeleton,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

interface ResourceDetailHeaderProps {
  parents: {
    to: string;
    label: string;
    isLoading?: boolean;
  }[];
  resourceName: string;
  description?: string;
  titleAddon?: ReactNode;
}

export const ResourceDetailHeader = ({
  resourceName,
  description,
  titleAddon,
  parents,
}: ResourceDetailHeaderProps) => {
  return (
    <Stack hasGutter>
      <StackItem>
        <Breadcrumb>
          {parents.map(({ to, label, isLoading }, index) => (
            <BreadcrumbItem
              key={index}
              render={({ className, ariaCurrent }) =>
                isLoading ? (
                  <Skeleton width="50px" />
                ) : (
                  <Link className={className} aria-current={ariaCurrent} to={to}>
                    {label}
                  </Link>
                )
              }
            />
          ))}
          <BreadcrumbItem isActive>{resourceName}</BreadcrumbItem>
        </Breadcrumb>
      </StackItem>

      <StackItem>
        <Stack hasGutter={false}>
          <StackItem>
            <Flex
              alignItems={{ default: 'alignItemsCenter' }}
              spaceItems={{ default: 'spaceItemsMd' }}
              flexWrap={{ default: 'wrap' }}
            >
              <FlexItem>
                <Title headingLevel="h1" size="2xl">
                  {resourceName}
                </Title>
              </FlexItem>
              {titleAddon ? <FlexItem>{titleAddon}</FlexItem> : null}
            </Flex>
          </StackItem>
          {description && (
            <StackItem>
              <Content component="p">{description}</Content>
            </StackItem>
          )}
        </Stack>
      </StackItem>
    </Stack>
  );
};
