import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  PageSection,
  PageSectionTypes,
  Title,
} from '@patternfly/react-core';

import { FieldValidationProvider } from '../../components/Form/FieldValidationContext';
import { SecurityGroupCreateWizard } from '../../components/networking/SecurityGroupCreateWizard';
import { useTranslation } from '../../hooks/useTranslation';

export const SecurityGroupCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Breadcrumb>
          <BreadcrumbItem>
            <Button variant="link" isInline onClick={() => navigate('/networking/security-groups')}>
              {t('Security groups')}
            </Button>
          </BreadcrumbItem>
          <BreadcrumbItem isActive>{t('Create security group')}</BreadcrumbItem>
        </Breadcrumb>
        <Title headingLevel="h1" size="3xl">
          {t('Create security group')}
        </Title>
      </PageSection>
      <PageSection
        hasBodyWrapper={false}
        isFilled
        type={PageSectionTypes.wizard}
        aria-label={t('Create security group wizard')}
      >
        <FieldValidationProvider>
          <SecurityGroupCreateWizard
            virtualNetworkId={searchParams.get('virtualNetworkId') ?? undefined}
          />
        </FieldValidationProvider>
      </PageSection>
    </>
  );
};

export default SecurityGroupCreatePage;
