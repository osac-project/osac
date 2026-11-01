import { Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import CatalogField from '../CatalogField';
import CatalogFieldPolicyDescription from '../CatalogFieldPolicyDescription';
import CatalogUserDataInput from '../CatalogUserDataInput';

const VMCatalogAccessStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Access & Initial run')}
        </Title>
      </StackItem>
      <StackItem>
        <CatalogFieldPolicyDescription
          description={t(
            'Configure optional user data for virtual machines created from this catalog item, or skip this step to continue.',
          )}
        />
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="userData">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <CatalogUserDataInput
                valueName={valueName}
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default VMCatalogAccessStep;
