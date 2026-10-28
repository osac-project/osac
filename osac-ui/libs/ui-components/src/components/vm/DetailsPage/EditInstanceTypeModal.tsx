import { useState } from 'react';
import {
  Alert,
  Button,
  Content,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import {
  type ComputeInstance,
  ComputeInstanceState,
  type InstanceType,
  InstanceTypeState,
  InstanceTypes,
} from '@osac/types';

import { cel } from '../../../api/cel';
import { useInstanceType } from '../../../api/v1/instance-types';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import OsacForm from '../../Form/OsacForm';
import {
  type ResourceListItem,
  ResourceSelectField,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../Form/ResourceSelectField';
import { useUpdateVmInstanceType } from '../useUpdateVmInstanceType';
import { formatInstanceTypeOptionLabel } from '../utils';

interface EditInstanceTypeModalProps {
  vm: ComputeInstance;
  onClose: () => void;
}

interface FormValues {
  instanceType: ResourceSelectValue;
}

type CurrentTypeStatus = 'loading' | 'ready' | 'error';

const hasGpu = (item: { spec?: { gpu?: unknown } } | undefined): boolean =>
  Boolean(item?.spec?.gpu);

const validationSchema = (t: TFunction) =>
  Yup.object({
    instanceType: Yup.object({
      id: Yup.string().required(t('An instance type is required')),
    }),
  });

const EditInstanceTypeModal = ({ vm, onClose }: EditInstanceTypeModalProps) => {
  const { t } = useTranslation();
  const { updateInstanceType } = useUpdateVmInstanceType();
  const {
    data: currentType,
    isLoading,
    isError,
    refetch,
  } = useInstanceType(vm.spec?.instanceType?.id);
  const [submitError, setSubmitError] = useState<unknown>();

  const currentTypeStatus: CurrentTypeStatus = isLoading
    ? 'loading'
    : isError || !currentType
      ? 'error'
      : 'ready';
  const stopped = vm.status?.state === ComputeInstanceState.STOPPED;
  const initialInstanceType =
    currentTypeStatus === 'ready' && currentType
      ? {
          id: currentType.id,
          label: currentType.metadata?.name || currentType.id,
        }
      : undefined;
  const initialValue = initialInstanceType
    ? { id: initialInstanceType.id, name: initialInstanceType.label }
    : emptyResourceSelectValue();
  const listFilter = cel<InstanceType>((filter) =>
    filter.or(
      filter.field('spec.state').equals(InstanceTypeState.ACTIVE),
      currentType?.id ? filter.field('id').equals(currentType.id) : undefined,
    ),
  );

  const handleSubmit = async (values: FormValues) => {
    if (
      !values.instanceType.id ||
      currentTypeStatus !== 'ready' ||
      values.instanceType.id === initialInstanceType?.id
    ) {
      return;
    }
    setSubmitError(undefined);
    try {
      await updateInstanceType(
        vm,
        values.instanceType.id,
        values.instanceType.name || values.instanceType.id,
      );
      onClose();
    } catch (error) {
      setSubmitError(error);
    }
  };

  return (
    <Formik<FormValues>
      enableReinitialize
      initialValues={{ instanceType: initialValue }}
      validationSchema={validationSchema(t)}
      onSubmit={handleSubmit}
    >
      {({ submitForm, isSubmitting, values }) => {
        const selectionDiffers =
          Boolean(values.instanceType.id) && values.instanceType.id !== initialInstanceType?.id;
        const saveDisabled =
          isSubmitting ||
          currentTypeStatus !== 'ready' ||
          !values.instanceType.id ||
          !selectionDiffers;

        return (
          <Modal
            variant="small"
            isOpen
            onClose={isSubmitting ? undefined : onClose}
            aria-labelledby="edit-instance-type-modal-title"
          >
            <ModalHeader title={t('Edit instance type')} labelId="edit-instance-type-modal-title" />
            <ModalBody>
              <Content component="p">
                {t('Select a new instance type for this virtual machine.')}
              </Content>
              {currentTypeStatus === 'error' ? (
                <Alert
                  variant="danger"
                  isInline
                  title={t('Could not load the current instance type')}
                  actionLinks={
                    <Button variant="link" onClick={() => void refetch()}>
                      {t('Retry')}
                    </Button>
                  }
                />
              ) : null}
              <OsacForm>
                <ResourceSelectField
                  name="instanceType"
                  label={t('Instance type')}
                  fieldId="edit-instance-type"
                  service={InstanceTypes}
                  request={{ filter: listFilter }}
                  isRequired
                  isDisabled={currentTypeStatus !== 'ready'}
                  filterItems={(item: ResourceListItem) => {
                    if (currentTypeStatus !== 'ready' || !currentType) {
                      return true;
                    }
                    return hasGpu(item as InstanceType) === hasGpu(currentType);
                  }}
                  formatOptionLabel={(item) => formatInstanceTypeOptionLabel(item as InstanceType)}
                  placeholder={t('Select an instance type')}
                  loadErrorTitle={t('Error loading instance types')}
                  emptyTitle={t('No instance types available')}
                />
              </OsacForm>
              {stopped && selectionDiffers ? (
                <Alert variant="info" isInline title={t('Resize will apply on next start.')} />
              ) : null}
              {submitError ? (
                <Alert variant="danger" isInline title={t('Failed to update the instance type')}>
                  {getErrorMessage(submitError)}
                </Alert>
              ) : null}
            </ModalBody>
            <ModalFooter>
              <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
                {t('Cancel')}
              </Button>
              <Button
                variant="primary"
                onClick={submitForm}
                isDisabled={saveDisabled}
                isLoading={isSubmitting}
              >
                {t('Save')}
              </Button>
            </ModalFooter>
          </Modal>
        );
      }}
    </Formik>
  );
};

export default EditInstanceTypeModal;
