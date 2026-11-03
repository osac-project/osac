import { useState } from 'react';
import {
  Alert,
  Divider,
  Flex,
  FlexItem,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import VmDetailsActionButtons from './VmDetailsActionButtons';
import VmDetailsSections from './VmDetailsSections';
import { useDiskImage } from '../../../api/v1/disk-image';
import { useInstanceType } from '../../../api/v1/instance-types';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { VmStatusLabel } from '../../../VmStatusLabel';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import VmConsoleTab from '../console/VmConsoleTab';

interface Props {
  vm: ComputeInstance;
}

const VmDetails = ({ vm }: Props) => {
  const { t } = useTranslation();
  const [showConsole, setShowConsole] = useState(false);
  const {
    data: instanceType,
    isLoading: isInstanceTypeLoading,
    error: instanceTypeError,
  } = useInstanceType(vm.spec?.instanceType?.id);
  const { data: diskImage, isLoading: isDiskImageLoading } = useDiskImage(vm.spec?.diskImage?.id);

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
                  parents={[
                    {
                      to: '/vms',
                      label: t('Virtual machines'),
                    },
                  ]}
                  resourceName={vm.metadata?.name ?? vm.id}
                  description={t(
                    'Review configuration, networking, storage, and utilization for this virtual machine.',
                  )}
                  titleAddon={<VmStatusLabel state={vm.status?.state} />}
                />
              </FlexItem>
              <FlexItem>
                <VmDetailsActionButtons vm={vm} onOpenConsole={() => setShowConsole(true)} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <VmDetailsSections
        vm={vm}
        instanceType={instanceType}
        isInstanceTypeLoading={isInstanceTypeLoading}
        diskImage={diskImage}
        isDiskImageLoading={isDiskImageLoading}
      />
      {showConsole ? (
        <PageSection hasBodyWrapper={false}>
          <VmConsoleTab vm={vm} />
        </PageSection>
      ) : null}
    </>
  );
};

export default VmDetails;
