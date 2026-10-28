import { PageSection, Stack, StackItem } from '@patternfly/react-core';

import type { ComputeInstance, DiskImage, InstanceType } from '@osac/types';

import VmDetailsCard from './VmDetailsCard';
import VmExternalIpSection from './VmExternalIpSection';
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
}: VmDetailsSectionsProps) => (
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
        <VmExternalIpSection vm={vm} />
      </StackItem>
      <StackItem>
        <VmNetworkingSection vm={vm} />
      </StackItem>
      <StackItem>
        <VmStorageSection vm={vm} />
      </StackItem>
      {vm.spec?.userData?.trim() ? (
        <StackItem>
          <VmUserDataCard vm={vm} />
        </StackItem>
      ) : null}
    </Stack>
  </PageSection>
);

export default VmDetailsSections;
