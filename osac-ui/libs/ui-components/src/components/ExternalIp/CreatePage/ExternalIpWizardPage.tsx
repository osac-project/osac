import { useNavigate } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ExternalIpWizard from './ExternalIpWizard';
import { EXTERNAL_IPS_LIST_PATH } from './values';

const ExternalIpWizardPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate(EXTERNAL_IPS_LIST_PATH)}>
                {t('External IPs')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Create')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Create external IP')}
          </Title>
        </Stack>
      </PageSection>
      <ExternalIpWizard />
    </>
  );
};

export default ExternalIpWizardPage;
