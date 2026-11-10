import {
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';
import { RhUiRobotIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-robot-icon';

import type { ServiceAccount } from '@osac/types';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';

import ServiceAccountStatusLabel from './ServiceAccountStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceNameField from '../Resource/ResourceNameField';

interface ServiceAccountCardProps {
  serviceAccount: ServiceAccount;
}

const ServiceAccountCard = ({ serviceAccount }: ServiceAccountCardProps) => {
  const { t } = useTranslation();

  return (
    <ListPageCard
      title={<ResourceNameField resource={serviceAccount} />}
      icon={<RhUiRobotIcon />}
      statusLabel={<ServiceAccountStatusLabel serviceAccount={serviceAccount} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={serviceAccount.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </ListPageCard>
  );
};

export default ServiceAccountCard;
