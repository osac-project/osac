import { Divider, PageSection, Stack, StackItem } from '@patternfly/react-core';

import type { ComputeInstance, DiskImage, InstanceType } from '@osac/types';

import VmDetailsCard from './VmDetailsCard';
import VmNetworkingSection from './VmNetworkingSection';
import VmStorageSection from './VmStorageSection';

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
      </Stack>
    </PageSection>
  );
};

export default VmDetailsSections;
