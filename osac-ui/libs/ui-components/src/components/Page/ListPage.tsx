import * as React from 'react';
import {
  Bullseye,
  Content,
  Flex,
  FlexItem,
  PageSection,
  Spinner,
  Stack,
  Title,
} from '@patternfly/react-core';

import type { UsePermissionsResult } from '@osac/ui-components/hooks/use-permissions';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import QueryErrorState from '../Resource/QueryErrorState';

type ListPageProps = {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  breadcrumb?: React.ReactNode;
  error?: unknown;
  permissions?: UsePermissionsResult;
  children: React.ReactNode;
};

const PermissionDeniedState = () => {
  const { t } = useTranslation();
  const permissionDeniedMessage = t('You are not authorized to access this resource.');

  return (
    <QueryErrorState
      error={permissionDeniedMessage}
      mode="page"
      headingLevel="h2"
      title={t('Permission denied')}
      body={permissionDeniedMessage}
      status="warning"
    />
  );
};

const ListPage: React.FC<ListPageProps> = ({
  title,
  description,
  actions,
  breadcrumb,
  error,
  permissions,
  children,
}) => {
  let content = children;
  if (permissions?.isLoading) {
    content = (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  } else if (permissions?.error) {
    content = <QueryErrorState error={permissions.error} mode="page" headingLevel="h2" />;
  } else if (permissions && !permissions.allowed) {
    content = <PermissionDeniedState />;
  }

  return (
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
      {content}
    </PageSection>
  );
};

export default ListPage;
