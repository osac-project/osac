import {
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';
import RhUiConnectedIcon from '@patternfly/react-icons/dist/esm/icons/rh-ui-connected-icon';

import type { IdentityProvider } from '@osac/types';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';

import IdentityProviderActionsMenu from './IdentityProviderActionsMenu';
import IdentityProviderStatusLabel from './IdentityProviderStatusLabel';
import { getIdpIssuerUrl, getIdpName, resolveIdpType } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import TruncatedText from '../Primitives/TruncatedText';
import ResourceNameField from '../Resource/ResourceNameField';

interface IdentityProviderCardProps {
  identityProvider: IdentityProvider;
}

const IdentityProviderCard = ({ identityProvider }: IdentityProviderCardProps) => {
  const { t } = useTranslation();
  const issuerUrl = getIdpIssuerUrl(identityProvider);

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={identityProvider}
          title={getIdpName(identityProvider)}
          subTitle={identityProvider.metadata?.tenant}
        />
      }
      icon={<RhUiConnectedIcon />}
      statusLabel={<IdentityProviderStatusLabel idp={identityProvider} />}
      actionMenu={<IdentityProviderActionsMenu idp={identityProvider} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Enabled')}</DescriptionListTerm>
            <DescriptionListDescription>
              {identityProvider.spec?.enabled ? t('Enabled') : t('Disabled')}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Protocol')}</DescriptionListTerm>
            <DescriptionListDescription>
              {resolveIdpType(t, identityProvider.spec?.config.case)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Issuer URL')}</DescriptionListTerm>
            <DescriptionListDescription>
              {issuerUrl ? <TruncatedText content={issuerUrl} maxCharsDisplayed={48} /> : '-'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={identityProvider.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </ListPageCard>
  );
};

export default IdentityProviderCard;
