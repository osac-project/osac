import { FC, ReactNode } from 'react';
import { CardBody, CardFooter, Divider, Stack, StackItem } from '@patternfly/react-core';
import { RhUiClusterIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-cluster-icon';
import { RhUiServerStackIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-server-stack-icon';
import { RhUiVirtualServerIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-virtual-server-icon';

import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';

type InstanceKind = 'vm' | 'bm' | 'cluster';

interface InstanceCardProps {
  kind?: InstanceKind;
  statusLabel: ReactNode;
  actionMenu: ReactNode;
  title: ReactNode;
  body: ReactNode;
  footer: ReactNode;
  actionButton?: ReactNode;
}

export const InstanceTypeIcon: FC<{ kind?: InstanceKind }> = ({ kind }) => {
  switch (kind) {
    case 'cluster':
      return <RhUiClusterIcon />;
    case 'bm':
      return <RhUiServerStackIcon />;
    case 'vm':
      return <RhUiVirtualServerIcon />;
      break;
    default:
      return null;
  }
};

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
    icon={<InstanceTypeIcon kind={kind} />}
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
