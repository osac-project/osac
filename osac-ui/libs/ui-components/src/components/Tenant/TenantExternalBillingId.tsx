import { useNavigate } from 'react-router-dom';
import { Button, Stack, StackItem } from '@patternfly/react-core';

import type { Tenant } from '@osac/types/private';

import { tenantBillingStepPath } from './routes';
import { useTranslation } from '../../hooks/useTranslation';

interface TenantExternalBillingIdProps {
  tenant: Tenant;
}

const TenantExternalBillingId = ({ tenant }: TenantExternalBillingIdProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const externalBillingId = tenant.spec?.externalBillingId;
  const tenantName = tenant.metadata?.name ?? tenant.id;
  const openBillingStep = () => navigate(tenantBillingStepPath(tenant.id));

  if (!externalBillingId) {
    return (
      <Button
        variant="link"
        isInline
        onClick={openBillingStep}
        aria-label={t('Add external billing ID for {{name}}', { name: tenantName })}
      >
        {t('Add external billing ID')}
      </Button>
    );
  }

  return (
    <Stack>
      <StackItem>{externalBillingId}</StackItem>
      <StackItem>
        <Button
          variant="link"
          isInline
          onClick={openBillingStep}
          aria-label={t('Edit external billing ID for {{name}}', { name: tenantName })}
        >
          {t('Edit')}
        </Button>
      </StackItem>
    </Stack>
  );
};

export default TenantExternalBillingId;
