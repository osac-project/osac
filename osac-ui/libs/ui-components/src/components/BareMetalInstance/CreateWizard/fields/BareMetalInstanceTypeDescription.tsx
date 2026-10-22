import {
  Card,
  CardBody,
  CardTitle,
  Gallery,
  GalleryItem,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { BareMetalInstanceType } from '@osac/types';

import { useTranslation } from '../../../../hooks/useTranslation';

interface BareMetalInstanceTypeDescriptionProps {
  instanceType: BareMetalInstanceType;
}

const BareMetalInstanceTypeDescription = ({
  instanceType,
}: BareMetalInstanceTypeDescriptionProps) => {
  const { t } = useTranslation();
  const accelerators = instanceType.spec?.hardware?.accelerators.map(
    (a) => `${a.type} (${a.model}${a.memoryGb ? `, ${a.memoryGb} GB` : ''})`,
  );
  const disks = instanceType.spec?.hardware?.disks.map((a) => `${a.type} (${a.capacityGb})`);

  return (
    <Gallery hasGutter>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('CPU')}</CardTitle>
          <CardBody>
            <Stack>
              <StackItem>
                {t('Model: {{model}}', { model: instanceType.spec?.hardware?.cpu?.model || '-' })}
              </StackItem>
              <StackItem>
                {t('Architecture: {{arch}}', {
                  arch: instanceType.spec?.hardware?.cpu?.architecture || '-',
                })}
              </StackItem>
              <StackItem>
                {t('Cores: {{cores}}', { cores: instanceType.spec?.hardware?.cpu?.cores || '-' })}
              </StackItem>
              <StackItem>
                {t('Threads per core: {{threads}}', {
                  threads: instanceType.spec?.hardware?.cpu?.threadsPerCore || '-',
                })}
              </StackItem>
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('Memory')}</CardTitle>
          <CardBody>
            <Stack>
              <StackItem>
                {t('Total GB: {{total}}', {
                  total: instanceType.spec?.hardware?.memory?.totalGb || '-',
                })}
              </StackItem>
              <StackItem>
                {t('Type: {{type}}', { type: instanceType.spec?.hardware?.memory?.type || '-' })}
              </StackItem>
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('Accelerators')}</CardTitle>
          <CardBody>
            <Stack>
              {accelerators?.length
                ? accelerators.map((a, idx) => <StackItem key={idx}>{a}</StackItem>)
                : '-'}
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
      <GalleryItem>
        <Card variant="secondary" isFullHeight>
          <CardTitle>{t('Disks')}</CardTitle>
          <CardBody>
            <Stack>
              {disks?.length ? disks.map((d, idx) => <StackItem key={idx}>{d}</StackItem>) : '-'}
            </Stack>
          </CardBody>
        </Card>
      </GalleryItem>
    </Gallery>
  );
};

export default BareMetalInstanceTypeDescription;
