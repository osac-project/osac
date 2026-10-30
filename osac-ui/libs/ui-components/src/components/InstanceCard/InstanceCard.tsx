import { ReactNode } from 'react';
import { CardBody, CardFooter, Divider, Stack, StackItem } from '@patternfly/react-core';

import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import { CatalogItemIcon } from '@osac/ui-components/icons';

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
  <ListPageCard
    title={title}
    statusLabel={statusLabel}
    actionMenu={actionMenu}
    icon={<CatalogItemIcon kind={kind} />}
  >
    <CardBody>{body}</CardBody>
    {footer || actionButton ? <Divider /> : null}
    <CardFooter>
      <Stack hasGutter>
        {footer ? <StackItem>{footer}</StackItem> : null}
        {actionButton ? <StackItem>{actionButton}</StackItem> : null}
      </Stack>
    </CardFooter>
  </ListPageCard>
);

export default InstanceCard;
