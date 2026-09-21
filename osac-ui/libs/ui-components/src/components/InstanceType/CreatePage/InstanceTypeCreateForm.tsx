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

import { getInstanceTypeCreateSchema } from './validation';
import { InstanceTypeCreateFormValues, instanceTypeCreateValues } from './values';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import NameField from '../../catalogProvision/wizard/fields/NameField';
import { InputField } from '../../Form/InputField';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';
import OsacForm from '../../Form/OsacForm';

export const INSTANCE_TYPES_LIST_ROUTE = '/admin/infrastructure/instance-types';

const InstanceTypeCreateForm = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { mutateAsync: create, error } = useCreateResource(InstanceTypes);

  const onSubmit = async (values: InstanceTypeCreateFormValues) => {
    const { gpu } = values.spec;
    const hasGpu = Boolean(gpu.pciDeviceSelector || gpu.resourceName || gpu.count);

    try {
      const created = await create({
        object: {
          metadata: { name: values.metadata.name },
          spec: {
            description: values.spec.description,
            vcpus: Number(values.spec.vcpus),
            memoryGib: Number(values.spec.memoryGib),
            ...(hasGpu
              ? {
                  gpu: {
                    pciDeviceSelector: gpu.pciDeviceSelector,
                    resourceName: gpu.resourceName,
                    count: Number(gpu.count),
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
                <NameField />
                <InputField
                  name="spec.description"
                  label={t('Description')}
                  fieldId="instance-type-description"
                  multiline
                />
                <InputField
                  name="spec.vcpus"
                  label={t('vCPUs')}
                  fieldId="instance-type-vcpus"
                  type="number"
                  isRequired
                />
                <InputField
                  name="spec.memoryGib"
                  label={t('Memory (GiB)')}
                  fieldId="instance-type-memory-gib"
                  type="number"
                  isRequired
                />
                <FormSection title={t('GPU')}>
                  <InputField
                    name="spec.gpu.count"
                    label={t('GPU count')}
                    fieldId="instance-type-gpu-count"
                    type="number"
                    helperText={t('Number of GPU devices of this type.')}
                  />
                  <InputField
                    name="spec.gpu.resourceName"
                    label={t('Resource name')}
                    fieldId="instance-type-gpu-resource-name"
                    placeholder={t('e.g. nvidia.com/A100')}
                    helperText={t('Kubernetes device plugin resource name.')}
                  />
                  <InputField
                    name="spec.gpu.pciDeviceSelector"
                    label={t('PCI device selector')}
                    fieldId="instance-type-gpu-pci-device-selector"
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
