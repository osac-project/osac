import { useNavigate } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import VmCreateWizard from './VmCreateWizard';
import { useTranslation } from '../../../hooks/useTranslation';

const VmCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const navigateToParent = () => navigate('/vms');

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={navigateToParent}>
                {t('Virtual Machines')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Launch virtual machine')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Create a virtual machine')}
          </Title>
          <Content component="p">
            {t('Configure the virtual machine and submit it for provisioning.')}
          </Content>
        </Stack>
      </PageSection>
      <VmCreateWizard onClosed={navigateToParent} />
    </>
  );
};

export default VmCreatePage;
