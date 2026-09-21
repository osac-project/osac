import { useCallback, useMemo, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Bullseye,
  Button,
  PageSection,
  PageSectionTypes,
  Spinner,
  Title,
  Wizard,
  WizardStep,
} from '@patternfly/react-core';
import { Formik } from 'formik';

import { Secret, Secrets } from '@osac/types';
import {
  useCreateResource,
  useGetResource,
  useUpdateResource,
} from '@osac/ui-components/api/use-resource';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { buildSecretCreatePayload, buildSecretUpdatePayload } from './payload';
import SecretDataStep from './steps/SecretDataStep';
import SecretGeneralStep from './steps/SecretGeneralStep';
import SecretReviewStep from './steps/SecretReviewStep';
import { getSecretValidationSchema, secretStepHasErrors } from './validation';
import { type SecretValues, getSecretValues } from './values';
import { useTranslation } from '../../../hooks/useTranslation';
import { FieldValidationProvider } from '../../Form/FieldValidationContext';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '../../Wizard/OSACWizardFooter';
import { isTypeFilterValue } from '../utils';

type SecretCreateWizardSteps = 'general' | 'data' | 'review';

interface SecretCreateWizardProps {
  secret?: Secret;
}

const SecretCreateWizard = ({ secret }: SecretCreateWizardProps) => {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();

  const typeParam = searchParams.get('type');

  const navigate = useNavigate();
  const {
    mutateAsync: createAsync,
    error: createErr,
    reset: resetCreate,
  } = useCreateResource(Secrets);
  const {
    mutateAsync: updateAsync,
    error: updateErr,
    reset: resetUpdate,
  } = useUpdateResource(Secrets);
  const [currentStep, setCurrentStep] = useState<SecretCreateWizardSteps>('general');

  const handleErrorReset = useCallback(() => {
    resetCreate();
    resetUpdate();
  }, [resetCreate, resetUpdate]);

  const initialValues = useMemo(
    () => getSecretValues(typeParam && isTypeFilterValue(typeParam) ? typeParam : null, secret),
    // only on initial render
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const onSubmit = async (values: SecretValues) => {
    try {
      if (secret) {
        await updateAsync({
          object: {
            id: secret.id,
            ...buildSecretUpdatePayload(values),
          },
        });
        navigate(`/secrets/${secret.id}`);
      } else {
        const resp = await createAsync({ object: buildSecretCreatePayload(values) });
        navigate(resp.object?.id ? `/secrets/${resp.object.id}` : '/secrets');
      }
    } catch {
      // nothing to do, tanstack handles the error
    }
  };

  return (
    <Formik<SecretValues>
      initialValues={initialValues}
      validationSchema={getSecretValidationSchema(t, !!secret)}
      onSubmit={onSubmit}
    >
      <>
        <PageSection hasBodyWrapper={false}>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate('/secrets')}>
                {t('Secrets')}
              </Button>
            </BreadcrumbItem>
            {secret && (
              <BreadcrumbItem>
                <Button variant="link" isInline onClick={() => navigate(`/secrets/${secret.id}`)}>
                  {secret.metadata?.name}
                </Button>
              </BreadcrumbItem>
            )}
            <BreadcrumbItem isActive>
              {secret ? t('Edit secret') : t('Create secret')}
            </BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {secret ? t('Edit secret') : t('Create secret')}
          </Title>
        </PageSection>
        <FieldValidationProvider>
          <LeaveFormConfirmation />
          <PageSection
            hasBodyWrapper={false}
            type={PageSectionTypes.wizard}
            aria-label={t('Secret create wizard')}
          >
            <Wizard
              navAriaLabel={t('Create Secret steps')}
              isVisitRequired
              footer={
                <OSACWizardFooter
                  onCancel={() => navigate('/secrets')}
                  stepHasErrors={secretStepHasErrors}
                  isEdit={!!secret}
                  error={createErr || updateErr}
                  onErrorReset={handleErrorReset}
                />
              }
              onStepChange={(_, step) => {
                setCurrentStep(step.id as SecretCreateWizardSteps);
              }}
            >
              <WizardStep id="general" name={t('General')}>
                {currentStep === 'general' && <SecretGeneralStep isEdit={!!secret} />}
              </WizardStep>
              <WizardStep id="data" name={t('Secret data')}>
                {currentStep === 'data' && <SecretDataStep isEdit={!!secret} />}
              </WizardStep>
              <WizardStep id="review" name={t('Review')}>
                {currentStep === 'review' && <SecretReviewStep />}
              </WizardStep>
            </Wizard>
          </PageSection>
        </FieldValidationProvider>
      </>
    </Formik>
  );
};

const SecretCreatePage = () => {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();

  const { data, isLoading, error } = useGetResource(
    Secrets,
    { id: id ?? '' },
    { enabled: Boolean(id) },
  );

  if (isLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  if (error) {
    return (
      <Alert variant="danger" isInline title={t('Failed to fetch secret')}>
        {getErrorMessage(error)}
      </Alert>
    );
  }

  return <SecretCreateWizard secret={data?.object} />;
};

export default SecretCreatePage;
