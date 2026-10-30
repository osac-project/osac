import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from '../../../Form/constants';
import { InputField } from '../../../Form/InputField';
import OsacForm from '../../../Form/OsacForm';
import CatalogField from '../CatalogField';

const ComputeCatalogAccessStep = () => {
  const { t } = useTranslation();

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
              // Workaround: project-scoped catalog items are not supported in
              // the UI yet, so catalog item user data is limited to text.
              <InputField
                name={valueName}
                label={t('User data')}
                fieldId="compute-catalog-user-data"
                multiline
                rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
                resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
                helperText={t('Optional cloud-init user data (max 64 KB).')}
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
