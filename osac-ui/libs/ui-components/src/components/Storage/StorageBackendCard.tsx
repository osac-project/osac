import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
} from '@patternfly/react-core';
import { RhUiStorageIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-storage-icon';

import type { StorageBackend } from '@osac/types/private';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';

import StorageBackendActionsMenu from './StorageBackendActionsMenu';
import StorageBackendStatusLabel from './StorageBackendStatusLabel';
import { storageBackendDetailsPath } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { getStorageBackendProviderLabel } from '../../utils/storageBackendProvider';
import ResourceNameField from '../Resource/ResourceNameField';

interface StorageBackendCardProps {
  backend: StorageBackend;
}

const StorageBackendCard = ({ backend }: StorageBackendCardProps) => {
  const { t } = useTranslation();

  return (
    <ListPageCard
      title={
        <ResourceNameField resource={backend} detailsUrl={storageBackendDetailsPath(backend.id)} />
      }
      icon={<RhUiStorageIcon />}
      statusLabel={<StorageBackendStatusLabel state={backend.status?.state} />}
      actionMenu={<StorageBackendActionsMenu backend={backend} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Provider')}</DescriptionListTerm>
            <DescriptionListDescription>
              {getStorageBackendProviderLabel(backend.spec?.provider ?? '', t)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Endpoint')}</DescriptionListTerm>
            <DescriptionListDescription>{backend.spec?.endpoint}</DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <OsacCardFooterRow
          term={t('Created')}
          description={<Timestamp value={backend.metadata?.creationTimestamp} />}
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default StorageBackendCard;
