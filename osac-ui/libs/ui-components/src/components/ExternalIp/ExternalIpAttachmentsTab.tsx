import type { ReactNode } from 'react';
import { Stack, StackItem } from '@patternfly/react-core';

import { ExternalIPAttachments } from '@osac/types';

import ExternalIpAttachmentsTable from './ExternalIpAttachmentsTable';
import { useListAllResources } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import NetworkingListEmptyState from '../networking/NetworkingListEmptyState';
import NetworkingListToolbar from '../networking/NetworkingListToolbar';
import { useNetworkingListFilters } from '../networking/useNetworkingListFilters';
import ListPageBody from '../Page/ListPageBody';

interface ExternalIpAttachmentsTabProps {
  /** The page's Create attachment action, offered by the empty state. */
  createAction?: ReactNode;
}

/** Every ExternalIPAttachment: which external IP routes to which workload. */
const ExternalIpAttachmentsTab = ({ createAction }: ExternalIpAttachmentsTabProps) => {
  const { t } = useTranslation();
  const filters = useNetworkingListFilters();
  const { data: attachmentsReply, isLoading, error } = useListAllResources(ExternalIPAttachments);
  const attachments = attachmentsReply?.items ?? [];
  const filteredAttachments = attachments.filter((attachment) => filters.matches(attachment));

  return (
    <ListPageBody isLoading={isLoading} error={error}>
      <Stack hasGutter>
        <StackItem>
          <NetworkingListToolbar
            filters={filters}
            searchLabel={t('Search attachments by name')}
            shownCount={filteredAttachments.length}
            totalCount={attachments.length}
          />
        </StackItem>
        <StackItem>
          {filteredAttachments.length === 0 ? (
            <NetworkingListEmptyState
              isFiltered={filters.isFiltered}
              emptyTitle={t('No attachments')}
              emptyBody={t('Create an attachment to route an external IP to a workload.')}
              emptyAction={createAction}
              onClearFilters={filters.clearAll}
            />
          ) : (
            <ExternalIpAttachmentsTable attachments={filteredAttachments} />
          )}
        </StackItem>
      </Stack>
    </ListPageBody>
  );
};

export default ExternalIpAttachmentsTab;
