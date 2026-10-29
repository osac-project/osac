import {
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';
import KeyIcon from '@patternfly/react-icons/dist/esm/icons/key-icon';

import { Secret, SecretType } from '@osac/types';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import SecretActionsMenu from '@osac/ui-components/components/Secret/SecretActionsMenu';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { getSecretType, getSecretTypeDataKeys } from './utils.ts';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceNameField from '../Resource/ResourceNameField';

interface SecretCardProps {
  secret: Secret;
  onEdit: () => void;
  onDelete: () => void;
}

const SecretCard = ({ secret, onEdit, onDelete }: SecretCardProps) => {
  const { t } = useTranslation();
  const secretTypes = getSecretType(t);
  const typeLabel = secretTypes[secret.type] || secretTypes[SecretType.UNSPECIFIED];
  const dataKeys = getSecretTypeDataKeys(secret.type);

  return (
    <ListPageCard
      title={<ResourceNameField resource={secret} detailsUrl={`/secrets/${secret.id}`} />}
      icon={<KeyIcon />}
      actionMenu={<SecretActionsMenu secret={secret} onEdit={onEdit} onDelete={onDelete} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Type')}</DescriptionListTerm>
            <DescriptionListDescription>{typeLabel}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Keys')}</DescriptionListTerm>
            <DescriptionListDescription>{dataKeys || '-'}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Added')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={secret.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </ListPageCard>
  );
};

export default SecretCard;
