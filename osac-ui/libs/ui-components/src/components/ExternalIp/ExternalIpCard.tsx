import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
} from '@patternfly/react-core';
import { RhUiConnectedIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-connected-icon';

import type { ExternalIP, ExternalIPPool } from '@osac/types';
import type { ExternalIpAttachedTarget } from '@osac/ui-components/api/v1/external-ip-data';
import ExternalIpPool from '@osac/ui-components/components/ExternalIp/Details/ExternalIpPool';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';

import ExternalIpAttachedTo from './Details/ExternalIpAttachedTo';
import ExternalIpStatusLabel from './Details/ExternalIpStatusLabel';
import ExternalIpActionsMenu from './ExternalIpActionsMenu';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceNameField from '../Resource/ResourceNameField';

const EMPTY_STATE = '—';

interface ExternalIpCardProps {
  externalIp: ExternalIP;
  pool?: ExternalIPPool;
  attachedTarget?: ExternalIpAttachedTarget;
}

const ExternalIpCard = ({ externalIp, pool, attachedTarget }: ExternalIpCardProps) => {
  const { t } = useTranslation();
  const address = externalIp.status?.address || EMPTY_STATE;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={externalIp}
          title={externalIp.metadata?.name ?? externalIp.id}
        />
      }
      icon={<RhUiConnectedIcon />}
      statusLabel={<ExternalIpStatusLabel state={externalIp.status?.state} />}
      actionMenu={<ExternalIpActionsMenu externalIp={externalIp} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {externalIp.metadata?.project || t('Default')}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Attached to')}</DescriptionListTerm>
            <DescriptionListDescription>
              {externalIp.status?.attached && attachedTarget ? (
                <ExternalIpAttachedTo
                  attached={externalIp.status.attached}
                  target={attachedTarget}
                />
              ) : (
                EMPTY_STATE
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Address')}</DescriptionListTerm>
            <DescriptionListDescription>{address}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('IP pool')}</DescriptionListTerm>
            <DescriptionListDescription>
              <ExternalIpPool externalIp={externalIp} pool={pool} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <OsacCardFooterRow
          term={t('Created')}
          description={<Timestamp value={externalIp.metadata?.creationTimestamp} />}
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default ExternalIpCard;
