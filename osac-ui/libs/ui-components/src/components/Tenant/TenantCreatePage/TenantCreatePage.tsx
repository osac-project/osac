import { useNavigate } from 'react-router-dom';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Button,
  FormSection,
  PageSection,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { FieldArray, Formik } from 'formik';

import { Tenants } from '@osac/types/private';

import BreakGlassCredentialModal from './BreakGlassCredentialModal';
import { getTenantSchema } from './validation';
import { tenantValues } from './values';
import { useCreateResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import TextField from '../../Form/base/TextField';
import OsacForm from '../../Form/OsacForm';

const TenantCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data, mutate, error, isPending } = useCreateResource(Tenants);

  return (
    <>
      {data?.object && <BreakGlassCredentialModal tenant={data.object} />}
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate('/admin/tenants')}>
                {t('Tenants')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Create')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Create tenant')}
          </Title>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        <Formik
          initialValues={tenantValues}
          validationSchema={getTenantSchema(t)}
          onSubmit={(values) =>
            mutate({
              object: {
                metadata: { name: values.name },
                spec: { domains: values.domains },
              },
            })
          }
        >
          {({ values, submitForm }) => (
            <Stack hasGutter>
              <StackItem>
                <OsacForm>
                  <TextField name="name" label={t('Name')} fieldId="tenant-name" isRequired />
                  <FormSection title={t('Domains')}>
                    <FieldArray name="domains">
                      {(helpers) => (
                        <Stack hasGutter>
                          {values.domains.map((_domain, index) => (
                            <StackItem key={index}>
                              <TextField
                                name={`domains.${index}`}
                                label={t('Domain {{number}}', { number: index + 1 })}
                                fieldId={`tenant-domain-${index}`}
                                isRequired
                              >
                                <Button
                                  variant="plain"
                                  aria-label={t('Remove domain')}
                                  onClick={() => helpers.remove(index)}
                                  icon={<MinusCircleIcon />}
                                />
                              </TextField>
                            </StackItem>
                          ))}
                          <StackItem>
                            <Button
                              variant="link"
                              icon={<PlusCircleIcon />}
                              onClick={() => helpers.push('')}
                            >
                              {t('Add domain')}
                            </Button>
                          </StackItem>
                        </Stack>
                      )}
                    </FieldArray>
                  </FormSection>
                </OsacForm>
              </StackItem>

              {!!error && (
                <StackItem>
                  <Alert variant="danger" title={t('Failed to create tenant')} isInline>
                    {getErrorMessage(error)}
                  </Alert>
                </StackItem>
              )}
              <StackItem>
                <ActionList>
                  <ActionListGroup>
                    <ActionListItem>
                      <Button
                        variant="primary"
                        onClick={submitForm}
                        isDisabled={isPending}
                        isLoading={isPending}
                      >
                        {t('Create')}
                      </Button>
                    </ActionListItem>
                    <ActionListItem>
                      <Button
                        variant="link"
                        onClick={() => navigate('/admin/tenants')}
                        isDisabled={isPending}
                      >
                        {t('Cancel')}
                      </Button>
                    </ActionListItem>
                  </ActionListGroup>
                </ActionList>
              </StackItem>
            </Stack>
          )}
        </Formik>
      </PageSection>
    </>
  );
};

export default TenantCreatePage;
