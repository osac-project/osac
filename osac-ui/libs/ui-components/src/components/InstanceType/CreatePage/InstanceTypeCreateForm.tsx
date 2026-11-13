import { useNavigate } from 'react-router-dom';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
  Alert,
  Button,
  FormSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Formik } from 'formik';

import { InstanceTypes } from '@osac/types/private';
import { useCreateResource } from '@osac/ui-components/api/use-resource';

import { getInstanceTypeCreateSchema, isGpuConfigured } from './validation';
import { instanceTypeFieldIds } from '../fieldIds';
import { InstanceTypeCreateFormValues, instanceTypeCreateValues } from './values';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import IntegerField from '../../Form/base/IntegerField';
import TextAreaField from '../../Form/base/TextAreaField';
import TextField from '../../Form/base/TextField';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';

export const INSTANCE_TYPES_LIST_ROUTE = '/admin/infrastructure/instance-types';

const InstanceTypeCreateForm = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { mutateAsync: create, error } = useCreateResource(InstanceTypes);

  const onSubmit = async (values: InstanceTypeCreateFormValues) => {
    const { gpu } = values.spec;
    const hasGpu = isGpuConfigured(gpu);

    try {
      const created = await create({
        object: {
          metadata: { name: values.metadata.name },
          spec: {
            description: values.spec.description,
            vcpus: values.spec.vcpus,
            memoryGib: values.spec.memoryGib,
            ...(hasGpu
              ? {
                  gpu: {
                    pciDeviceSelector: gpu.pciDeviceSelector,
                    resourceName: gpu.resourceName,
                    count: gpu.count,
                  },
                }
              : {}),
          },
        },
      });
      navigate(`${INSTANCE_TYPES_LIST_ROUTE}/${created.object?.id}`);
    } catch {
      // tanstack handles the error
    }
  };

  return (
    <Formik
      initialValues={instanceTypeCreateValues}
      validationSchema={getInstanceTypeCreateSchema(t)}
      onSubmit={onSubmit}
    >
      {({ submitForm, isSubmitting }) => (
        <>
          <LeaveFormConfirmation />
          <Stack hasGutter>
            <StackItem>
              <OsacForm>
                <NameField fieldId={instanceTypeFieldIds.name} />
                <TextAreaField
                  name="spec.description"
                  label={t('Description')}
                  fieldId={instanceTypeFieldIds.description}
                />
                <IntegerField
                  name="spec.vcpus"
                  label={t('vCPUs')}
                  fieldId={instanceTypeFieldIds.vcpus}
                  isRequired
                />
                <IntegerField
                  name="spec.memoryGib"
                  label={t('Memory (GiB)')}
                  fieldId={instanceTypeFieldIds.memoryGib}
                  isRequired
                />
                <FormSection title={t('GPU')}>
                  <IntegerField
                    name="spec.gpu.count"
                    label={t('GPU count')}
                    fieldId={instanceTypeFieldIds.gpuCount}
                    helperText={t('Number of GPU devices of this type.')}
                  />
                  <TextField
                    name="spec.gpu.resourceName"
                    label={t('Resource name')}
                    fieldId={instanceTypeFieldIds.gpuResourceName}
                    placeholder={t('e.g. nvidia.com/A100')}
                    helperText={t('Kubernetes device plugin resource name.')}
                  />
                  <TextField
                    name="spec.gpu.pciDeviceSelector"
                    label={t('PCI device selector')}
                    fieldId={instanceTypeFieldIds.gpuPciDeviceSelector}
                    placeholder={t('e.g. 10DE:20B0')}
                    helperText={t('PCI device selector identifying the GPU hardware.')}
                  />
                </FormSection>
              </OsacForm>
            </StackItem>
            {!!error && (
              <StackItem>
                <Alert variant="danger" title={t('Failed to create instance type')} isInline>
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
                      isDisabled={isSubmitting}
                      isLoading={isSubmitting}
                    >
                      {t('Create')}
                    </Button>
                  </ActionListItem>
                  <ActionListItem>
                    <Button
                      variant="link"
                      onClick={() => navigate(INSTANCE_TYPES_LIST_ROUTE)}
                      isDisabled={isSubmitting}
                    >
                      {t('Cancel')}
                    </Button>
                  </ActionListItem>
                </ActionListGroup>
              </ActionList>
            </StackItem>
          </Stack>
        </>
      )}
    </Formik>
  );
};

export default InstanceTypeCreateForm;
