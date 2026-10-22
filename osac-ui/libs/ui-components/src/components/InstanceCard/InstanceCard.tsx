import { ReactNode } from 'react';
import {
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  CardTitle,
  Divider,
  Flex,
  FlexItem,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { CatalogItemIcon } from '@osac/ui-components/icons';

import './InstanceCard.css';

interface InstanceCardProps {
  kind?: 'vm' | 'bm' | 'cluster';
  statusLabel: ReactNode;
  actionMenu: ReactNode;
  title: ReactNode;
  body: ReactNode;
  footer: ReactNode;
  actionButton?: ReactNode;
}

const InstanceCard = ({
  kind,
  statusLabel,
  actionMenu,
  title,
  body,
  footer,
  actionButton,
}: InstanceCardProps) => (
  <Card isFullHeight className="instance-card">
    <CardHeader
      className="instance-card__header"
      actions={{
        actions: (
          <Flex
            alignItems={{ default: 'alignItemsCenter' }}
            spaceItems={{ default: 'spaceItemsSm' }}
          >
            <FlexItem>{statusLabel}</FlexItem>
            <FlexItem>{actionMenu}</FlexItem>
          </Flex>
        ),
      }}
    >
      <CatalogItemIcon kind={kind} />
    </CardHeader>
    <CardTitle>{title}</CardTitle>
    <CardBody>{body}</CardBody>
    {footer || actionButton ? <Divider /> : null}
    <CardFooter>
      <Stack hasGutter>
        {footer ? <StackItem>{footer}</StackItem> : null}
        {actionButton ? <StackItem>{actionButton}</StackItem> : null}
      </Stack>
    </CardFooter>
  </Card>
);

export default InstanceCard;
