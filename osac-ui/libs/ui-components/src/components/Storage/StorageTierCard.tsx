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

import type { StorageBackend, StorageTier } from '@osac/types/private';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';

import StorageTierActionsMenu from './StorageTierActionsMenu';
import { protocolLabel, tierBackendDisplayNames } from './storageTierBackendResolution';
import { StorageTierStatusLabel } from './StorageTierStatusLabel';
import { storageTierDetailsPath } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceNameField from '../Resource/ResourceNameField';

interface StorageTierCardProps {
  tier: StorageTier;
  backendsById: ReadonlyMap<string, StorageBackend>;
}

const StorageTierCard = ({ tier, backendsById }: StorageTierCardProps) => {
  const { t } = useTranslation();
  const protocolLabels = protocolLabel(t);

  return (
    <ListPageCard
      title={<ResourceNameField resource={tier} detailsUrl={storageTierDetailsPath(tier.id)} />}
      icon={<RhUiStorageIcon />}
      statusLabel={<StorageTierStatusLabel state={tier.status?.state} />}
      actionMenu={<StorageTierActionsMenu tier={tier} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Backends')}</DescriptionListTerm>
            <DescriptionListDescription>
              {tierBackendDisplayNames(tier, backendsById)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Protocol')}</DescriptionListTerm>
            <DescriptionListDescription>
              {tier.spec?.protocol !== undefined ? protocolLabels[tier.spec.protocol] : '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <OsacCardFooterRow
          term={t('Created')}
          description={<Timestamp value={tier.metadata?.creationTimestamp} />}
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default StorageTierCard;
