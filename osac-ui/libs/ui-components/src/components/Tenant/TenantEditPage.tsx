import { useParams, useSearchParams } from 'react-router-dom';
import { Alert, Bullseye, PageSection, Spinner } from '@patternfly/react-core';

import { Tenants } from '@osac/types/private';
import { useGetResource } from '@osac/ui-components/api/use-resource';

import TenantWizard, { type TenantWizardStartStep } from './CreateWizard/TenantWizard';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';

const START_STEP_BY_PARAM: Record<string, TenantWizardStartStep> = {
  billing: 'billing',
};

const TenantEditPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();

  const { data, isLoading, error } = useGetResource(Tenants, { id }, { enabled: Boolean(id) });

  const startStep = START_STEP_BY_PARAM[searchParams.get('step') ?? ''] ?? 'general';

  if (isLoading) {
    return (
      <PageSection hasBodyWrapper={false}>
        <Bullseye>
          <Spinner />
        </Bullseye>
      </PageSection>
    );
  }

  if (error) {
    return (
      <PageSection hasBodyWrapper={false}>
        <Alert variant="danger" isInline title={t('Failed to fetch tenant')}>
          {getErrorMessage(error)}
        </Alert>
      </PageSection>
    );
  }

  if (!data?.object) {
    return (
      <PageSection hasBodyWrapper={false}>
        <Alert variant="danger" isInline title={t('Tenant not found')}>
          {t('The tenant {{id}} does not exist.', { id })}
        </Alert>
      </PageSection>
    );
  }

  return <TenantWizard tenant={data.object} startStep={startStep} />;
};

export default TenantEditPage;
