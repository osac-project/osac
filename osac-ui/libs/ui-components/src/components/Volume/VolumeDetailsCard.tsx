import {
  Card,
  CardBody,
  CardTitle,
  ClipboardCopy,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  ExpandableSection,
} from '@patternfly/react-core';

import type { Volume } from '@osac/types';

import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { Timestamp } from '../Primitives/Timestamp';

interface VolumeDetailsCardProps {
  volume: Volume;
}

const formatSizeGib = (sizeGib: bigint | undefined): string => {
  if (sizeGib === undefined) {
    return '—';
  }
  return `${Number(sizeGib)} GiB`;
};

const VolumeDetailsCard = ({ volume }: VolumeDetailsCardProps) => {
  const { t } = useTranslation();

  const statusMessage = volume.status?.message;

  return (
    <Card isFullHeight>
      <CardTitle>{t('Identification')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('ID')}</DescriptionListTerm>
            <DescriptionListDescription>
              <ClipboardCopy isReadOnly hoverTip={t('Copy')} clickTip={t('Copied')}>
                {volume.id}
              </ClipboardCopy>
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(volume.metadata?.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>

      <CardTitle>{t('Configuration')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Storage Tier')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(volume.spec?.storageTier)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Size')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatSizeGib(volume.spec?.sizeGib)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Access Mode')}</DescriptionListTerm>
            <DescriptionListDescription>
              <VolumeAccessModeLabel accessMode={volume.spec?.accessMode} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>

      <CardTitle>{t('Status')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('State')}</DescriptionListTerm>
            <DescriptionListDescription>
              <VolumeStatusLabel state={volume.status?.state} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          {statusMessage !== undefined && statusMessage !== '' ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Message')}</DescriptionListTerm>
              <DescriptionListDescription>
                <ExpandableSection toggleText={statusMessage.slice(0, 80)}>
                  {statusMessage}
                </ExpandableSection>
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : null}
        </DescriptionList>
      </CardBody>

      <CardTitle>{t('Metadata')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(volume.metadata?.tenant)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(volume.metadata?.project)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={volume.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default VolumeDetailsCard;
