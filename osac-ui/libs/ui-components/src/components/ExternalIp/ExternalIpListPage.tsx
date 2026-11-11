import { useState } from 'react';
import { Tab, TabTitleText, Tabs } from '@patternfly/react-core';

import { ExternalIPAttachments, ExternalIPs } from '@osac/types';
import { usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { EXTERNAL_IPS_LIST_PATH } from './CreatePage/values';
import ExternalIpAddressesTab from './ExternalIpAddressesTab';
import ExternalIpAttachmentCreateModal from './ExternalIpAttachmentCreateModal';
import ExternalIpAttachmentsTab from './ExternalIpAttachmentsTab';
import ListPage from '../Page/ListPage';
import CreateButton from '../Primitives/CreateButton.tsx';

export const EXTERNAL_IPS_TAB_PARAM = 'tab';
const ATTACHMENTS_TAB = 'attachments';

/** External IPs with the Addresses and Attachments tabs (`?tab=attachments`). */
const ExternalIpListPage = () => {
  const { t } = useTranslation();
  const [tab, setTab] = usePageFilter(EXTERNAL_IPS_TAB_PARAM);
  const [isCreateAttachmentOpen, setIsCreateAttachmentOpen] = useState(false);
  const activeTab = tab === ATTACHMENTS_TAB ? ATTACHMENTS_TAB : 'addresses';
  const createAttachmentButton = (
    <CreateButton service={ExternalIPAttachments} onClick={() => setIsCreateAttachmentOpen(true)}>
      {t('Create attachment')}
    </CreateButton>
  );
  const createExternalIpButton = (
    <CreateButton service={ExternalIPs} to={`${EXTERNAL_IPS_LIST_PATH}/create`}>
      {t('Create external IP')}
    </CreateButton>
  );

  return (
    <ListPage
      title={t('External IPs')}
      description={t(
        'Addresses from shared provider pools that make workloads reachable from outside the virtual network.',
      )}
      actions={activeTab === ATTACHMENTS_TAB ? createAttachmentButton : createExternalIpButton}
    >
      <Tabs
        activeKey={activeTab}
        onSelect={(_event, key) => setTab(key === ATTACHMENTS_TAB ? ATTACHMENTS_TAB : '')}
        aria-label={t('External IP tabs')}
        mountOnEnter
        unmountOnExit
      >
        <Tab eventKey="addresses" title={<TabTitleText>{t('Addresses')}</TabTitleText>}>
          <div className="pf-v6-u-mt-md">
            <ExternalIpAddressesTab createAction={createExternalIpButton} />
          </div>
        </Tab>
        <Tab eventKey={ATTACHMENTS_TAB} title={<TabTitleText>{t('Attachments')}</TabTitleText>}>
          <div className="pf-v6-u-mt-md">
            <ExternalIpAttachmentsTab createAction={createAttachmentButton} />
          </div>
        </Tab>
      </Tabs>
      {isCreateAttachmentOpen ? (
        <ExternalIpAttachmentCreateModal onClose={() => setIsCreateAttachmentOpen(false)} />
      ) : null}
    </ListPage>
  );
};

export default ExternalIpListPage;
