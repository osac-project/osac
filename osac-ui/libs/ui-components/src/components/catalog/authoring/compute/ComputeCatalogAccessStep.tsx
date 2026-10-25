import { Content, Stack, StackItem, Title } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { ComputeCatalogItemValues } from './values';
import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import UserDataField from '../../../Form/UserDataField';
import CatalogField from '../CatalogField';

const ComputeCatalogAccessStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ComputeCatalogItemValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Access & Initial run')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t('Add optional user data, or skip this step to continue.')}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="userData">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <UserDataField
                sourceName="userDataSource.value"
                secretRefName="userDataSecret.value"
                inlineName={valueName}
                projectName={values.metadata.project}
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

export default ComputeCatalogAccessStep;
