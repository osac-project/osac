import { useNavigate } from 'react-router-dom';
import { Tab, TabTitleText, Tabs } from '@patternfly/react-core';

import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { StorageBackendsListPage } from './StorageBackendsListPage';
import { StorageTiersListPage } from './StorageTiersListPage';

type StorageTab = 'backends' | 'tiers';

const isStorageTab = (value: string | number): value is StorageTab =>
  value === 'backends' || value === 'tiers';

export const StorageManagementPage = ({ activeTab }: { activeTab: StorageTab }) => {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <ListPage
      title={t('Storage')}
      description={t('Manage storage backends and tiers for this cloud platform.')}
    >
      <ListPageBody isLoading={false}>
        <Tabs
          activeKey={activeTab}
          onSelect={(_event, tabKey) => {
            if (isStorageTab(tabKey)) {
              navigate(`/admin/infrastructure/storage/${tabKey}`, { replace: true });
            }
          }}
          aria-label={t('Storage tabs')}
          mountOnEnter
          unmountOnExit
        >
          <Tab eventKey="backends" title={<TabTitleText>{t('Backends')}</TabTitleText>}>
            <StorageBackendsListPage />
          </Tab>
          <Tab eventKey="tiers" title={<TabTitleText>{t('Tiers')}</TabTitleText>}>
            <StorageTiersListPage />
          </Tab>
        </Tabs>
      </ListPageBody>
    </ListPage>
  );
};
