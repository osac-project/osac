import { useState } from 'react';
import {
  Alert,
  Button,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { type ClusterVersion, Clusters } from '@osac/types';

import { useUpdateResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { getApiErrorMessage } from '../../../utils/error';
import OsacForm from '../../Form/OsacForm';
import { SelectField } from '../../Form/SelectField';
import { isDeprecatedVersion, versionDisplayName } from '../CreateWizard/versionUtils';

export interface UpgradeClusterDialogProps {
  clusterId: string;
  currentVersion?: string;
  eligibleVersions: ClusterVersion[];
  onClose: () => void;
}

interface FormValues {
  targetVersionId: string;
}

const validationSchema = (t: TFunction) =>
  Yup.object({ targetVersionId: Yup.string().required(t('A target version is required')) });

const UpgradeClusterDialog = ({
  clusterId,
  currentVersion,
  eligibleVersions,
  onClose,
}: UpgradeClusterDialogProps) => {
  const { t } = useTranslation();
  const updateCluster = useUpdateResource(Clusters, { retry: false });
  const [submitError, setSubmitError] = useState<unknown>();
  const options = eligibleVersions.map((version) => {
    const label = versionDisplayName(version, version.id);
    return {
      value: version.id,
      label: isDeprecatedVersion(version)
        ? t('{{version}} (deprecated)', { version: label })
        : label,
    };
  });

  const handleSubmit = async ({ targetVersionId }: FormValues) => {
    if (!eligibleVersions.some((version) => version.id === targetVersionId)) {
      return;
    }
    setSubmitError(undefined);
    try {
      await updateCluster.mutateAsync({
        object: { id: clusterId, spec: { version: { id: targetVersionId } } },
        updateMask: { paths: ['spec.version'] },
      });
      onClose();
    } catch (error) {
      setSubmitError(error);
    }
  };

  return (
    <Formik<FormValues>
      initialValues={{ targetVersionId: '' }}
      validationSchema={validationSchema(t)}
      onSubmit={handleSubmit}
    >
      {({ submitForm, isSubmitting, values }) => {
        const pending = isSubmitting || updateCluster.isPending;
        const selectedTargetExists = eligibleVersions.some(
          (version) => version.id === values.targetVersionId,
        );

        return (
          <Modal
            variant="small"
            isOpen
            onClose={pending ? undefined : onClose}
            aria-labelledby="upgrade-cluster-dialog-title"
          >
            <ModalHeader title={t('Upgrade cluster')} labelId="upgrade-cluster-dialog-title" />
            <ModalBody>
              <DescriptionList isCompact>
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Current version')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    {currentVersion?.trim() || '—'}
                  </DescriptionListDescription>
                </DescriptionListGroup>
              </DescriptionList>
              <OsacForm>
                <SelectField
                  name="targetVersionId"
                  label={t('Target version')}
                  fieldId="upgrade-cluster-target-version"
                  options={options}
                  isRequired
                  isDisabled={pending || eligibleVersions.length === 0}
                  placeholder={t('Select a target version')}
                  helperText={t('Choose a target version to continue.')}
                  popperProps={{ appendTo: 'inline' }}
                />
              </OsacForm>
              {eligibleVersions.length === 0 ? (
                <Alert variant="warning" isInline title={t('No upgrade targets are available.')} />
              ) : null}
              <Content component="p">
                {t(
                  'The control plane upgrades first. Node sets automatically follow to the same version.',
                )}
              </Content>
              <Button
                component="a"
                variant="link"
                isInline
                href="https://access.redhat.com/labs/ocpupgradegraph/update_path/"
                target="_blank"
                rel="noopener noreferrer"
              >
                {t('Check upgrade path and risks')}
              </Button>
              <Alert
                variant="warning"
                isInline
                title={t('An accepted upgrade cannot be cancelled or rolled back.')}
              />
              {submitError ? (
                <Alert
                  variant="danger"
                  isInline
                  isLiveRegion
                  title={t('Could not request the cluster upgrade')}
                >
                  {getApiErrorMessage(submitError, t)}
                </Alert>
              ) : null}
            </ModalBody>
            <ModalFooter>
              <Button variant="link" onClick={onClose} isDisabled={pending}>
                {t('Cancel')}
              </Button>
              <Button
                variant="primary"
                onClick={submitForm}
                isDisabled={pending || !selectedTargetExists}
                isLoading={pending}
              >
                {t('Upgrade cluster')}
              </Button>
            </ModalFooter>
          </Modal>
        );
      }}
    </Formik>
  );
};

export default UpgradeClusterDialog;
