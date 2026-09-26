import { useState } from 'react';
import {
  Bullseye,
  Button,
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
  DropdownList,
  MenuToggle,
  Spinner,
} from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';
import { PlusCircleIcon } from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';

import { type NATGateway, NATGatewayState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import { NatGatewayStatusLabel } from './NatGatewayStatusLabel';
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
}

const NatGatewayCard = ({
  natAddress,
  natGateway,
  isLoading = false,
  error,
  onAttach,
  onDetach,
}: NatGatewayCardProps) => {
  const { t } = useTranslation();
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  const isDeleting = natGateway?.status?.state === NATGatewayState.NAT_GATEWAY_STATE_DELETING;

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
                  aria-label={t('Actions for NAT gateway attachment')}
                >
                  <EllipsisVIcon />
                </MenuToggle>
              )}
              popperProps={{ position: 'right' }}
            >
              <DropdownList>
                <DeleteResourceButton
                  isDropdown
                  canDelete={!isDeleting}
                  onClick={() => {
                    onDetach(natGateway);
                    setIsMenuOpen(false);
                  }}
                />
              </DropdownList>
            </Dropdown>
          ) : !isLoading && !error ? (
            <Button variant="link" isInline icon={<PlusCircleIcon />} onClick={onAttach}>
              {t('Create')}
            </Button>
          ) : undefined,
        }}
      >
        <CardTitle>{t('NAT gateway attachment')}</CardTitle>
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
          <DescriptionList isCompact aria-label={t('Attached NAT gateway')}>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
              <DescriptionListDescription>
                <NatGatewayStatusLabel state={natGateway.status?.state} />
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
              <DescriptionListDescription>
                {natGateway.metadata?.name ?? natGateway.id}
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('External IP')}</DescriptionListTerm>
              <DescriptionListDescription>
                <code>{natAddress ?? '—'}</code>
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Attached')}</DescriptionListTerm>
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
