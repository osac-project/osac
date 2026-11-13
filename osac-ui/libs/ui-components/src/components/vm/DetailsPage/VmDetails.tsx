import {
  Alert,
  Divider,
  Flex,
  FlexItem,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { type ComputeInstance, InstanceTypes } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';

import VmDetailsActionButtons from './VmDetailsActionButtons';
import VmDetailsSections from './VmDetailsSections';
import { useDiskImage } from '../../../api/v1/disk-image';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { VmStatusLabel } from '../../../VmStatusLabel';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';

interface Props {
  vm: ComputeInstance;
}

const VmDetails = ({ vm }: Props) => {
  const { t } = useTranslation();
  const id = vm.spec?.instanceType?.id;
  const {
    data: instanceType,
    isLoading: isInstanceTypeLoading,
    error: instanceTypeError,
  } = useGetResource(InstanceTypes, { id }, { enabled: !!id });
  const {
    data: diskImage,
    isLoading: isDiskImageLoading,
    error: diskImageError,
  } = useDiskImage(vm.spec?.diskImage?.id);
  const openConsole = () => {
    window.open(`/vms/${encodeURIComponent(vm.id)}/console`, '_blank', 'noopener,noreferrer');
  };

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
          {diskImageError ? (
            <StackItem>
              <Alert variant="danger" title={t('Could not load disk image')} isInline>
                {getErrorMessage(diskImageError)}
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
                <VmDetailsActionButtons vm={vm} onOpenConsole={openConsole} />
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
        instanceType={instanceType?.object}
        isInstanceTypeLoading={isInstanceTypeLoading}
        diskImage={diskImage}
        isDiskImageLoading={isDiskImageLoading}
      />
    </>
  );
};

export default VmDetails;
