import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  PageSection,
  PageSectionTypes,
  Title,
  Wizard,
} from '@patternfly/react-core';
import type { WizardProps } from '@patternfly/react-core';
import { Formik, type FormikValues } from 'formik';
import type { AnyObjectSchema } from 'yup';

import { useTranslation } from '../../../hooks/useTranslation';
import { FieldValidationProvider } from '../../Form/FieldValidationContext';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';

interface CatalogItemWizardShellProps<TValues extends FormikValues, TStepId extends string> {
  initialValues: TValues;
  validationSchema: AnyObjectSchema | undefined;
  onSubmit: (values: TValues) => Promise<void>;
  title: string;
  wizardAriaLabel: string;
  navAriaLabel: string;
  onStepChange: (stepId: TStepId) => void;
  footer: NonNullable<WizardProps['footer']>;
  children: ReactNode;
}

const CatalogItemWizardShell = <TValues extends FormikValues, TStepId extends string>({
  initialValues,
  validationSchema,
  onSubmit,
  title,
  wizardAriaLabel,
  navAriaLabel,
  onStepChange,
  footer,
  children,
}: CatalogItemWizardShellProps<TValues, TStepId>) => {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <Formik<TValues>
      initialValues={initialValues}
      validationSchema={validationSchema}
      onSubmit={onSubmit}
    >
      <>
        <PageSection hasBodyWrapper={false}>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate('/catalog')}>
                {t('Catalog')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{title}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {title}
          </Title>
        </PageSection>
        <FieldValidationProvider>
          <LeaveFormConfirmation />
          <PageSection
            hasBodyWrapper={false}
            type={PageSectionTypes.wizard}
            aria-label={wizardAriaLabel}
          >
            <Wizard
              navAriaLabel={navAriaLabel}
              isVisitRequired
              footer={footer}
              onStepChange={(_, step) => onStepChange(step.id as TStepId)}
            >
              {children}
            </Wizard>
          </PageSection>
        </FieldValidationProvider>
      </>
    </Formik>
  );
};

export default CatalogItemWizardShell;
