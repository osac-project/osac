import {
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';
import VolumeIcon from '@patternfly/react-icons/dist/esm/icons/volume-icon';

import type { Volume } from '@osac/types';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { formatSizeGib } from './utils';
import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import VolumeActionsMenu from './VolumeActionsMenu';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { Timestamp } from '../Primitives/Timestamp';

interface VolumeCardProps {
  volume: Volume;
}

const VolumeCard = ({ volume }: VolumeCardProps) => {
  const { t } = useTranslation();

  return (
    <ListPageCard
      title={<ResourceNameField resource={volume} detailsUrl={`/storage/volumes/${volume.id}`} />}
      icon={<VolumeIcon />}
      actionMenu={<VolumeActionsMenu volume={volume} />}
      statusLabel={<VolumeStatusLabel state={volume.status?.state} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {volume.metadata?.project || t('Default')}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Storage Tier')}</DescriptionListTerm>
            <DescriptionListDescription>
              {volume.spec?.storageTier ?? '—'}
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
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={volume.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </ListPageCard>
  );
};

export default VolumeCard;
