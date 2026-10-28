import { FC, ReactNode } from 'react';
import { Card, CardHeader, CardTitle, Flex, FlexItem, Icon } from '@patternfly/react-core';

import './ListPageCard.css';

interface ListPageCardProps {
  title: ReactNode;
  icon: ReactNode;
  actionMenu?: ReactNode;
  statusLabel?: ReactNode;
  children: ReactNode;
}

const ListPageCard: FC<ListPageCardProps> = ({
  title,
  icon,
  statusLabel,
  actionMenu,
  children,
}) => {
  return (
    <Card isFullHeight>
      <CardHeader
        className="list-page-card__header"
        actions={{
          actions: (
            <Flex
              alignItems={{ default: 'alignItemsCenter' }}
              spaceItems={{ default: 'spaceItemsSm' }}
            >
              {statusLabel ? <FlexItem>{statusLabel}</FlexItem> : null}
              {actionMenu ? <FlexItem>{actionMenu}</FlexItem> : null}
            </Flex>
          ),
        }}
      >
        <span className="list-page-card__icon" aria-hidden>
          <Icon size="xl">{icon}</Icon>
        </span>
      </CardHeader>
      <CardTitle>{title}</CardTitle>
      {children}
    </Card>
  );
};

export default ListPageCard;
