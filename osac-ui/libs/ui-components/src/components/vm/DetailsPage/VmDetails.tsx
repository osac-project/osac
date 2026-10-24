import { useState } from 'react';
import {
  Alert,
  Divider,
  Flex,
  FlexItem,
  PageSection,
  Stack,
  StackItem,
  Tab,
  TabContent,
  TabContentBody,
  TabTitleText,
  Tabs,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import VmDetailsOverviewTab from './VmDetailsOverviewTab';
import VmDetailsSummary from './VmDetailsSummary';
import VmNetworkingTab from './VmNetworkingTab';
import { useInstanceType } from '../../../api/v1/instance-types';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { VmStatusLabel } from '../../../VmStatusLabel';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import VmActionButtons from '../actions/VmActionButtons';
import VmConsoleTab from '../console/VmConsoleTab';
import {
  VM_DETAIL_CONSOLE_TAB_ID,
  VM_DETAIL_NETWORKING_TAB_ID,
  VM_DETAIL_OVERVIEW_TAB_ID,
} from '../vm-detail-tabs';

interface Props {
  vm: ComputeInstance;
}

const VmDetails = ({ vm }: Props) => {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState(0);
  const {
    data: instanceType,
    isLoading: isInstanceTypeLoading,
    error: instanceTypeError,
  } = useInstanceType(vm.spec?.instanceType?.id);

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          {instanceTypeError ? (
            <StackItem>
              <Alert variant="danger" title={t('Could not load instance types')} isInline>
                {getErrorMessage(instanceTypeError)}
              </Alert>
            </StackItem>
          ) : null}
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parentTo="/vms"
                  parentLabel={t('Virtual machines')}
                  resourceName={vm.metadata?.name ?? vm.id}
                  titleAddon={<VmStatusLabel state={vm.status?.state} />}
                />
              </FlexItem>
              <FlexItem>
                <VmActionButtons vm={vm} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <VmDetailsSummary
              vm={vm}
              instanceType={instanceType}
              isInstanceTypeLoading={isInstanceTypeLoading}
            />
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
          <StackItem>
            <Tabs
              id="vm-detail-tabs"
              activeKey={activeTab}
              onSelect={(_e, key) => setActiveTab(Number(key))}
            >
              <Tab
                eventKey={0}
                title={<TabTitleText>{t('Overview')}</TabTitleText>}
                tabContentId={VM_DETAIL_OVERVIEW_TAB_ID}
              />
              <Tab
                eventKey={1}
                title={<TabTitleText>{t('Networking')}</TabTitleText>}
                tabContentId={VM_DETAIL_NETWORKING_TAB_ID}
              />
              <Tab
                eventKey={2}
                title={<TabTitleText>{t('Console')}</TabTitleText>}
                tabContentId={VM_DETAIL_CONSOLE_TAB_ID}
              />
            </Tabs>
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <TabContent
          eventKey={0}
          id={VM_DETAIL_OVERVIEW_TAB_ID}
          activeKey={activeTab}
          hidden={activeTab !== 0}
        >
          <TabContentBody>
            <VmDetailsOverviewTab vm={vm} />
          </TabContentBody>
        </TabContent>
        <TabContent
          eventKey={1}
          id={VM_DETAIL_NETWORKING_TAB_ID}
          activeKey={activeTab}
          hidden={activeTab !== 1}
        >
          <TabContentBody>
            <VmNetworkingTab vm={vm} />
          </TabContentBody>
        </TabContent>
        {activeTab === 2 ? (
          <TabContent eventKey={2} id={VM_DETAIL_CONSOLE_TAB_ID} activeKey={activeTab}>
            <TabContentBody>
              <VmConsoleTab vm={vm} />
            </TabContentBody>
          </TabContent>
        ) : null}
      </PageSection>
    </>
  );
};

export default VmDetails;
