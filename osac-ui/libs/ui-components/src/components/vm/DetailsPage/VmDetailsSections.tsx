import { Divider, PageSection, Stack, StackItem } from '@patternfly/react-core';

import type { ComputeInstance, DiskImage, InstanceType } from '@osac/types';

import VmDetailsCard from './VmDetailsCard';
import VmNetworkingSection from './VmNetworkingSection';
import VmStorageSection from './VmStorageSection';
import VmUserDataCard from './VmUserDataCard';

interface VmDetailsSectionsProps {
  vm: ComputeInstance;
  instanceType?: InstanceType;
  isInstanceTypeLoading: boolean;
  diskImage?: DiskImage;
  isDiskImageLoading: boolean;
}

const VmDetailsSections = ({
  vm,
  instanceType,
  isInstanceTypeLoading,
  diskImage,
  isDiskImageLoading,
}: VmDetailsSectionsProps) => {
  const userDataSource = vm.spec?.userDataSource?.source;
  const hasInlineUserData =
    userDataSource?.case === 'inline' && Boolean(userDataSource.value.trim());

  return (
    <PageSection hasBodyWrapper={false}>
      <Stack hasGutter>
        <StackItem>
          <VmDetailsCard
            vm={vm}
            instanceType={instanceType}
            isInstanceTypeLoading={isInstanceTypeLoading}
            diskImage={diskImage}
            isDiskImageLoading={isDiskImageLoading}
          />
        </StackItem>
        <StackItem>
          <Divider />
        </StackItem>
        <StackItem>
          <VmNetworkingSection vm={vm} />
        </StackItem>
        <StackItem>
          <Divider />
        </StackItem>
        <StackItem>
          <VmStorageSection vm={vm} />
        </StackItem>
        {hasInlineUserData ? (
          <>
            <StackItem>
              <Divider />
            </StackItem>
            <StackItem>
              <VmUserDataCard vm={vm} />
            </StackItem>
          </>
        ) : null}
      </Stack>
    </PageSection>
  );
};

export default VmDetailsSections;
