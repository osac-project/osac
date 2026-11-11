import { Stack, StackItem, Title } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import NameField from '@osac/ui-components/components/Form/NameField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import ProjectField from '@osac/ui-components/components/Form/ProjectField';
import TenantSelectField from '@osac/ui-components/components/Form/TenantSelectField';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import type { ExternalIpFormValues } from './values';

const ExternalIpGeneralStep = () => {
  const { t } = useTranslation();
  const { role } = useSession();
  const isAdmin = role === 'admin';
  const { values, setFieldValue } = useFormikContext<ExternalIpFormValues>();
  const tenantId = values.metadata.tenant?.id ?? '';

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('General')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          {/* A provider admin picks the tenant first; the projects are that tenant's. */}
          {isAdmin ? (
            <TenantSelectField onSelect={() => void setFieldValue('metadata.project', '', false)} />
          ) : null}
          <ProjectField tenant={isAdmin ? tenantId : undefined} isDisabled={isAdmin && !tenantId} />
          <NameField />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ExternalIpGeneralStep;
