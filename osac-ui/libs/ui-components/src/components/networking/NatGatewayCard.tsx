import { useState } from 'react';
import {
  Bullseye,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Dropdown,
  DropdownItem,
  DropdownList,
  MenuToggle,
  Spinner,
} from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';
import { PlusCircleIcon } from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';

import { type NATGateway, NATGateways } from '@osac/types';

import { useNatGatewayDeleteDisabledReason } from './deleteDisabledReasons';
import GatedCreateButton from './GatedCreateButton';
import { NatGatewayStatusLabel } from './NatGatewayStatusLabel';
import NetworkingResourceName from './NetworkingResourceName';
import StatusWithMessage from './StatusWithMessage';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import QueryErrorState from '../Resource/QueryErrorState';

export interface NatGatewayCardProps {
  natAddress?: string;
  natGateway?: NATGateway;
  isLoading?: boolean;
  error?: unknown;
  onAttach: () => void;
  onDetach: (natGateway: NATGateway) => void;
  /** Hides Create, e.g. on a tenant default network, which keeps its default NAT gateway. */
  isAttachHidden?: boolean;
  /** When set, Create is disabled and this text says what to do first. */
  attachDisabledReason?: string;
}

const NatGatewayCard = ({
  natAddress,
  natGateway,
  isLoading = false,
  error,
  onAttach,
  onDetach,
  isAttachHidden = false,
  attachDisabledReason,
}: NatGatewayCardProps) => {
  const { t } = useTranslation();
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  // The default NAT gateway, and one that clusters on its network use, cannot be deleted.
  const deleteDisabledReason = useNatGatewayDeleteDisabledReason(natGateway);

  return (
    <Card variant="secondary">
      <CardHeader
        actions={{
          actions: natGateway ? (
            <Dropdown
              isOpen={isMenuOpen}
              onOpenChange={setIsMenuOpen}
              toggle={(ref) => (
                <MenuToggle
                  ref={ref}
                  variant="plain"
                  onClick={() => setIsMenuOpen((open) => !open)}
                  isExpanded={isMenuOpen}
                  aria-label={t('Actions for {{name}}', {
                    name: natGateway.metadata?.name || natGateway.id,
                  })}
                >
                  <EllipsisVIcon />
                </MenuToggle>
              )}
              popperProps={{ position: 'right' }}
            >
              <DropdownList>
                <DropdownItem
                  isAriaDisabled={Boolean(deleteDisabledReason)}
                  tooltipProps={
                    deleteDisabledReason ? { content: deleteDisabledReason } : undefined
                  }
                  onClick={() => {
                    if (deleteDisabledReason) {
                      return;
                    }
                    onDetach(natGateway);
                    setIsMenuOpen(false);
                  }}
                >
                  {t('Delete')}
                </DropdownItem>
              </DropdownList>
            </Dropdown>
          ) : !isLoading && !error && !isAttachHidden ? (
            <GatedCreateButton
              service={NATGateways}
              variant="link"
              isInline
              icon={<PlusCircleIcon />}
              onClick={onAttach}
              disabledReason={attachDisabledReason}
            >
              {t('Create NAT gateway')}
            </GatedCreateButton>
          ) : undefined,
        }}
      >
        <CardTitle>{t('NAT gateway')}</CardTitle>
      </CardHeader>
      <Divider />
      <CardBody>
        {isLoading ? (
          <Bullseye>
            <Spinner aria-label={t('Loading NAT gateway')} />
          </Bullseye>
        ) : error ? (
          <QueryErrorState error={error} title={t('Failed to load NAT gateway')} />
        ) : natGateway ? (
          <DescriptionList isCompact aria-label={t('NAT gateway details')}>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
              <DescriptionListDescription>
                <StatusWithMessage message={natGateway.status?.message}>
                  <NatGatewayStatusLabel state={natGateway.status?.state} />
                </StatusWithMessage>
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
              <DescriptionListDescription>
                <NetworkingResourceName resource={natGateway} />
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('External IP')}</DescriptionListTerm>
              <DescriptionListDescription>
                <code>{natAddress ?? '—'}</code>
                <Content component="small">{t('Reachable where your provider routes it.')}</Content>
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
              <DescriptionListDescription>
                <Timestamp value={natGateway.metadata?.creationTimestamp} />
              </DescriptionListDescription>
            </DescriptionListGroup>
          </DescriptionList>
        ) : (
          <Content component="p">
            {t('No NAT gateway associated with this virtual network.')}
          </Content>
        )}
      </CardBody>
    </Card>
  );
};

export default NatGatewayCard;
