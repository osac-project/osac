import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';

import type { BareMetalInstance } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceLink from '../Resource/ResourceLink';

interface Props {
  instance: BareMetalInstance;
}

const BareMetalDetailsCard = ({ instance }: Props) => {
  const { t } = useTranslation();

  return (
    <Card isFullHeight>
      <CardTitle>{t('Details')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(instance.metadata?.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(instance.spec?.catalogItem?.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('SSH public key')}</DescriptionListTerm>
            <DescriptionListDescription>
              {instance.spec?.sshKey?.id ? (
                <ResourceLink
                  basePath="/secrets"
                  id={instance.spec.sshKey.id}
                  name={instance.spec.sshKey.name}
                />
              ) : (
                '—'
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={instance.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Creator')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(instance.metadata?.creator)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default BareMetalDetailsCard;
