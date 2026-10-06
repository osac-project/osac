import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { ExternalIPPools } from '@osac/types';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import { ResourceSelectField } from '@osac/ui-components/components/Form/ResourceSelectField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const ExternalIpConfigurationStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Select an assigned IP pool. The platform allocates the next available address automatically.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <ResourceSelectField
            name="pool"
            label={t('IP pool')}
            fieldId="create-external-ip-pool"
            service={ExternalIPPools}
            isRequired
            autoSelectSingleOption
            placeholder={t('Select an external IP pool')}
            loadErrorTitle={t('Error loading external IP pools')}
            emptyTitle={t('No external IP pools available')}
            emptyDescription={t(
              'Contact your administrator to have an external IP pool provisioned.',
            )}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ExternalIpConfigurationStep;
