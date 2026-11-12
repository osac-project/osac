import { Stack, StackItem, Title } from '@patternfly/react-core';

import { Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import TextField from '@osac/ui-components/components/Form/base/TextField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import SecretSelectionField from '@osac/ui-components/components/Form/SecretSelectionField';

import { useTranslation } from '../../../../hooks/useTranslation';

const IdpConfigurationStep = () => {
  const { t } = useTranslation();
  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('OIDC configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          <TextField
            name="spec.config.clientId"
            label={t('Client ID')}
            fieldId="idp-client-id"
            isRequired
          />
          <SecretSelectionField
            filter={cel<Secret>((filter) =>
              filter.and(
                filter.field('metadata.project').equals(''),
                filter.field('type').equals(SecretType.VALUE),
              ),
            )}
            label={t('Client secret secret')}
            name="spec.config.clientSecretSecret.name"
            isRequired
          />
          <TextField
            name="spec.config.issuer"
            label={t('Issuer')}
            fieldId="idp-issuer"
            isRequired
          />
          <TextField
            name="spec.config.authorizationUrl"
            label={t('Authorization URL')}
            fieldId="idp-authorization-url"
            isRequired
          />
          <TextField
            name="spec.config.tokenUrl"
            label={t('Token URL')}
            fieldId="idp-token-url"
            isRequired
          />
          <TextField
            name="spec.config.defaultScopes"
            label={t('Default scopes')}
            fieldId="idp-default-scopes"
            helperText={t('Space-separated list of scopes (e.g. openid profile email)')}
          />
          <TextField
            name="spec.config.userInfoUrl"
            label={t('User info URL')}
            fieldId="idp-user-info-url"
          />
          <TextField name="spec.config.jwksUrl" label={t('JWKS URL')} fieldId="idp-jwks-url" />
          <TextField
            name="spec.config.logoutUrl"
            label={t('Logout URL')}
            fieldId="idp-logout-url"
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default IdpConfigurationStep;
