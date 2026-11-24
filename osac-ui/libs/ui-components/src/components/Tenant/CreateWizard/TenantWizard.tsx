import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { WizardStep } from '@patternfly/react-core';

import { type Tenant, Tenants } from '@osac/types/private';
import { useCreateResource, useUpdateResource } from '@osac/ui-components/api/use-resource';
import ResourceWizardShell from '@osac/ui-components/components/Resource/ResourceWizardShell';

import { buildTenantCreatePayload, buildTenantUpdatePayload } from './payload';
import TenantBillingStep from './steps/TenantBillingStep';
import TenantGeneralStep from './steps/TenantGeneralStep';
import TenantReviewStep from './steps/TenantReviewStep';
import { getTenantSchema, tenantStepHasErrors } from './validation';
import { type TenantValues, getTenantValues } from './values';
import { useTranslation } from '../../../hooks/useTranslation';
import { OSACWizardFooter } from '../../Wizard/OSACWizardFooter';
import { TENANTS_LIST_PATH } from '../routes';

export type TenantWizardStartStep = 'general' | 'billing';

interface TenantWizardProps {
  tenant?: Tenant;
  startStep?: TenantWizardStartStep;
  onCreated?: (tenant: Tenant) => void;
}

const START_INDEX: Record<TenantWizardStartStep, number> = {
  general: 1,
  billing: 2,
};

const TenantWizard = ({ tenant, startStep = 'general', onCreated }: TenantWizardProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [currentStep, setCurrentStep] = useState<string>(
    startStep === 'billing' ? 'billing' : 'general',
  );

  const [initialValues] = useState(() => getTenantValues(tenant));

  const {
    mutateAsync: createTenant,
    error: createError,
    reset: resetCreateError,
  } = useCreateResource(Tenants);
  const {
    mutateAsync: updateTenant,
    error: updateError,
    reset: resetUpdateError,
  } = useUpdateResource(Tenants);

  const handleErrorReset = useCallback(() => {
    resetCreateError();
    resetUpdateError();
  }, [resetCreateError, resetUpdateError]);

  const onSubmit = async (values: TenantValues) => {
    try {
      if (tenant) {
        const payload = buildTenantUpdatePayload(values, initialValues);
        if (Object.keys(payload.spec ?? {}).length === 0) {
          navigate(TENANTS_LIST_PATH);
          return;
        }

        await updateTenant({ object: { id: tenant.id, ...payload } });
        navigate(TENANTS_LIST_PATH);
        return;
      }

      const created = await createTenant({ object: buildTenantCreatePayload(values) });
      if (created.object) {
        onCreated?.(created.object);
      }
    } catch {
      // The footer alert shows the mutation error.
    }
  };

  return (
    <ResourceWizardShell<TenantValues, string>
      initialValues={initialValues}
      validationSchema={getTenantSchema(t)}
      onSubmit={onSubmit}
      parentTo={TENANTS_LIST_PATH}
      parentLabel={t('Tenants')}
      title={tenant ? t('Edit tenant') : t('Register tenant')}
      navAriaLabel={t('Tenant steps')}
      startIndex={START_INDEX[startStep]}
      onStepChange={setCurrentStep}
      footer={
        <OSACWizardFooter
          onCancel={() => navigate(TENANTS_LIST_PATH)}
          stepHasErrors={tenantStepHasErrors}
          isEdit={Boolean(tenant)}
          submitLabel={tenant ? t('Save') : t('Register tenant')}
          disableSubmitWhenPristine={Boolean(tenant)}
          errorTitle={tenant ? t('Unable to save tenant') : t('Unable to register tenant')}
          error={createError || updateError}
          onErrorReset={handleErrorReset}
        />
      }
    >
      <WizardStep id="general" name={t('General')}>
        {currentStep === 'general' && <TenantGeneralStep isEdit={Boolean(tenant)} />}
      </WizardStep>
      <WizardStep id="billing" name={t('Billing')}>
        {currentStep === 'billing' && <TenantBillingStep />}
      </WizardStep>
      <WizardStep id="review" name={t('Review')}>
        {currentStep === 'review' && (
          <TenantReviewStep tenant={tenant} initialValues={initialValues} />
        )}
      </WizardStep>
    </ResourceWizardShell>
  );
};

export default TenantWizard;
