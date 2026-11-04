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
import { CheckboxField } from '../../Form/CheckboxField';
import OsacForm from '../../Form/OsacForm';
import { SelectField } from '../../Form/SelectField';
import { isDeprecatedVersion, versionDisplayName } from '../CreateWizard/versionUtils';

export interface UpgradeClusterDialogProps {
  clusterId: string;
  currentVersion?: string;
  eligibleVersions: ClusterVersion[];
  workerNodeCount: number;
  onClose: () => void;
}

interface FormValues {
  targetVersionId: string;
  acknowledgeDowntime: boolean;
}

const validationSchema = (t: TFunction) =>
  Yup.object({ targetVersionId: Yup.string().required(t('A target version is required')) });

const UpgradeClusterDialog = ({
  clusterId,
  currentVersion,
  eligibleVersions,
  workerNodeCount,
  onClose,
}: UpgradeClusterDialogProps) => {
  const { t } = useTranslation();
  const updateCluster = useUpdateResource(Clusters, { retry: false });
  const requiresDowntimeAcknowledgement = workerNodeCount === 1;
  const options = eligibleVersions.map((version) => {
    const label = versionDisplayName(version, version.id);
    return {
      value: version.id,
      label: isDeprecatedVersion(version)
        ? t('{{version}} (deprecated)', { version: label })
        : label,
    };
  });

  const handleSubmit = async ({ targetVersionId, acknowledgeDowntime }: FormValues) => {
    if (
      !eligibleVersions.some((version) => version.id === targetVersionId) ||
      (requiresDowntimeAcknowledgement && !acknowledgeDowntime)
    ) {
      return;
    }
    updateCluster.reset();
    try {
      await updateCluster.mutateAsync({
        object: { id: clusterId, spec: { version: { id: targetVersionId } } },
        updateMask: { paths: ['spec.version'] },
      });
      onClose();
    } catch {
      return;
    }
  };

  return (
    <Formik<FormValues>
      initialValues={{ targetVersionId: '', acknowledgeDowntime: false }}
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
                  <DescriptionListDescription>{currentVersion || '—'}</DescriptionListDescription>
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
                {requiresDowntimeAcknowledgement && (
                  <>
                    <Alert
                      variant="warning"
                      isInline
                      isLiveRegion
                      title={t(
                        'Workloads may be unavailable while the single worker node upgrades.',
                      )}
                    />
                    <CheckboxField
                      name="acknowledgeDowntime"
                      label={t('I understand this may cause downtime')}
                      fieldId="upgrade-cluster-downtime-acknowledgement"
                      isRequired
                      isDisabled={pending}
                    />
                  </>
                )}
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
              {updateCluster.error ? (
                <Alert
                  variant="danger"
                  isInline
                  isLiveRegion
                  title={t('Could not request the cluster upgrade')}
                >
                  {getApiErrorMessage(updateCluster.error, t)}
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
                isDisabled={
                  pending ||
                  !selectedTargetExists ||
                  (requiresDowntimeAcknowledgement && !values.acknowledgeDowntime)
                }
                isLoading={pending || undefined}
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
