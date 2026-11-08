import { useState } from 'react';
import {
  Alert,
  Button,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import {
  type BareMetalInstance,
  BareMetalInstanceState,
  BareMetalInstances,
  type Cluster,
  ClusterState,
  Clusters,
  type ComputeInstance,
  ComputeInstanceState,
  ComputeInstances,
  type ExternalIP,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachments,
  ExternalIPs,
  ServiceTier,
} from '@osac/types';

import { useCreateResource, useInvalidateServiceQueries } from '../../api/use-resource';
import { useExternalIpConsumers } from '../../api/v1/external-ip-data';
import { projectScopeFilter, unallocatedExternalIpFilter } from '../../api/v1/networking';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import OsacForm from '../Form/OsacForm';
import { RadioButtonField } from '../Form/RadioButtonField';
import {
  ResourceSelectField,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../Form/ResourceSelectField';

type TargetKind = 'computeInstance' | 'baremetalInstance' | 'cluster';
type Endpoint = 'api' | 'ingress';

interface FormValues {
  externalIp: ResourceSelectValue;
  targetKind: TargetKind;
  /** One selection per target kind, so switching the kind never keeps a stale target. */
  targets: Record<TargetKind, ResourceSelectValue>;
  endpoint: Endpoint;
}

type Target = ComputeInstance | BareMetalInstance | Cluster;

const validationSchema = (t: TFunction) =>
  Yup.object({
    externalIp: Yup.object({ id: Yup.string().required(t('An external IP is required')) }),
    targets: Yup.object().when('targetKind', ([kind]: TargetKind[], schema) =>
      schema.shape({
        [kind]: Yup.object({ id: Yup.string().required(t('A target is required')) }),
      }),
    ),
  });

// The server rejects a target that is not ready, so only ready targets are offered.
const isReadyTarget = (kind: TargetKind, target: Target): boolean => {
  switch (kind) {
    case 'computeInstance':
      return (target as ComputeInstance).status?.state === ComputeInstanceState.RUNNING;
    case 'baremetalInstance':
      return (target as BareMetalInstance).status?.state === BareMetalInstanceState.RUNNING;
    case 'cluster':
      return (target as Cluster).status?.state === ClusterState.READY;
  }
};

const TARGET_SERVICE_TIERS: Record<TargetKind, ServiceTier> = {
  computeInstance: ServiceTier.VMAAS,
  baremetalInstance: ServiceTier.BMAAS,
  cluster: ServiceTier.CAAS,
};

const TARGET_SERVICES = {
  computeInstance: ComputeInstances,
  baremetalInstance: BareMetalInstances,
  cluster: Clusters,
} as const;

interface ExternalIpAttachmentCreateModalProps {
  onClose: () => void;
}

/** Attaches an unused external IP to a ready virtual machine, bare-metal instance or Cluster. */
const ExternalIpAttachmentCreateModal = ({ onClose }: ExternalIpAttachmentCreateModalProps) => {
  const { t } = useTranslation();
  const { role, enabledServices } = useSession();
  const consumers = useExternalIpConsumers();
  const invalidateService = useInvalidateServiceQueries();
  const createAttachment = useCreateResource(ExternalIPAttachments, {
    onSuccess: async () => invalidateService(ExternalIPs),
  });
  const [externalIp, setExternalIp] = useState<ExternalIP>();
  // The attachment gets the external IP's project; targets must be in its project and tenant.
  const project = externalIp?.metadata?.project;

  const targetLabels: Record<TargetKind, string> = {
    computeInstance: t('Virtual machine'),
    baremetalInstance: t('Bare-metal instance'),
    cluster: t('Cluster'),
  };
  // Only the workload services this deployment enables can be targets.
  const targetKinds = (Object.keys(TARGET_SERVICE_TIERS) as TargetKind[]).filter((kind) =>
    enabledServices.includes(TARGET_SERVICE_TIERS[kind]),
  );

  return (
    <Formik<FormValues>
      initialValues={{
        externalIp: emptyResourceSelectValue(),
        targetKind: targetKinds[0] ?? 'computeInstance',
        targets: {
          computeInstance: emptyResourceSelectValue(),
          baremetalInstance: emptyResourceSelectValue(),
          cluster: emptyResourceSelectValue(),
        },
        endpoint: 'api',
      }}
      validationSchema={validationSchema(t)}
      onSubmit={async (values) => {
        try {
          await createAttachment.mutateAsync({
            object: {
              metadata: {
                name: `eipa-${crypto.randomUUID()}`,
                project,
                // A provider admin creates the attachment in the external IP's tenant.
                ...(role === 'admin' && externalIp?.metadata?.tenant
                  ? { tenant: externalIp.metadata.tenant }
                  : {}),
              },
              spec: {
                externalIp: { id: values.externalIp.id },
                target: {
                  case: values.targetKind,
                  value: { id: values.targets[values.targetKind].id },
                },
                ...(values.targetKind === 'cluster'
                  ? {
                      targetEndpoint:
                        values.endpoint === 'api'
                          ? ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
                          : ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
                    }
                  : {}),
              },
            },
          });
          onClose();
        } catch {
          // Surfaced via createAttachment.error below
        }
      }}
    >
      {({ values, submitForm, isSubmitting, setFieldValue }) => {
        // Clearing the External IP field resets only the Formik value, so the selection
        // counts only while that value is set. The root project's name is "".
        const selectedIp = values.externalIp.id ? externalIp : undefined;
        const ipProject = selectedIp ? (selectedIp.metadata?.project ?? '') : undefined;
        const ipTenant = selectedIp?.metadata?.tenant;
        return (
          <Modal
            variant="small"
            isOpen
            onClose={isSubmitting ? undefined : onClose}
            aria-labelledby="create-attachment-modal-title"
          >
            <ModalHeader title={t('Create attachment')} labelId="create-attachment-modal-title" />
            <ModalBody>
              <Stack hasGutter>
                {consumers.error ? (
                  <StackItem>
                    <Alert variant="danger" title={t('Error loading external IPs')} isInline>
                      {getErrorMessage(consumers.error)}
                    </Alert>
                  </StackItem>
                ) : null}
                <StackItem>
                  <OsacForm>
                    <ResourceSelectField<ExternalIP>
                      name="externalIp"
                      label={t('External IP')}
                      fieldId="create-attachment-external-ip"
                      service={ExternalIPs}
                      request={{ filter: unallocatedExternalIpFilter() }}
                      filterItems={consumers.isSelectable}
                      loadAllPages
                      formatOptionLabel={(ip) =>
                        `${ip.metadata?.name || ip.id} · ${ip.status?.address || '—'}`
                      }
                      isDisabled={consumers.isLoading || Boolean(consumers.error)}
                      isRequired
                      placeholder={t('Select an external IP')}
                      loadErrorTitle={t('Error loading external IPs')}
                      emptyTitle={t('No unattached external IPs available')}
                      emptyDescription={t('Create an external IP first, then attach it.')}
                      description={t('Reachable where your provider routes it.')}
                      onSelectResource={(_value, ip) => {
                        setExternalIp(ip);
                        // An earlier failure was about the previous selection.
                        createAttachment.reset();
                        // A target picked for another external IP may be outside its project.
                        void setFieldValue('targets', {
                          computeInstance: emptyResourceSelectValue(),
                          baremetalInstance: emptyResourceSelectValue(),
                          cluster: emptyResourceSelectValue(),
                        });
                      }}
                    />
                    <RadioButtonField
                      name="targetKind"
                      label={t('Target type')}
                      fieldId="create-attachment-target-kind"
                      isRequired
                      options={targetKinds.map((kind) => ({
                        value: kind,
                        label: targetLabels[kind],
                      }))}
                    />
                    <ResourceSelectField<Target>
                      key={values.targetKind}
                      name={`targets.${values.targetKind}`}
                      label={t('Target')}
                      fieldId="create-attachment-target"
                      service={TARGET_SERVICES[values.targetKind]}
                      // The server returns only the external IP's project (and, for a
                      // provider admin, tenant), so the first page holds usable targets.
                      request={
                        ipProject !== undefined
                          ? {
                              filter: projectScopeFilter(
                                ipProject,
                                role === 'admin' ? ipTenant : undefined,
                              ),
                            }
                          : undefined
                      }
                      // Readiness is checked here, so every page is loaded, but only once the
                      // external IP gives the project to filter by.
                      loadAllPages
                      isQueryEnabled={ipProject !== undefined}
                      filterItems={(target) =>
                        ipProject !== undefined &&
                        isReadyTarget(values.targetKind, target) &&
                        (target.metadata?.project ?? '') === ipProject &&
                        (role !== 'admin' || !ipTenant || target.metadata?.tenant === ipTenant)
                      }
                      isDisabled={!selectedIp}
                      isRequired
                      placeholder={
                        selectedIp ? t('Select a target') : t('Select an external IP first')
                      }
                      loadErrorTitle={t('Error loading targets')}
                      emptyTitle={t('No ready targets in this project')}
                      description={t("Only ready targets in the external IP's project are listed.")}
                    />
                    {values.targetKind === 'cluster' ? (
                      <RadioButtonField
                        name="endpoint"
                        label={t('Cluster endpoint')}
                        fieldId="create-attachment-endpoint"
                        isRequired
                        options={[
                          { value: 'api', label: t('API') },
                          { value: 'ingress', label: t('Ingress') },
                        ]}
                      />
                    ) : null}
                  </OsacForm>
                </StackItem>
                {createAttachment.error ? (
                  <StackItem>
                    <Alert variant="danger" title={t('Failed to create attachment')} isInline>
                      {getErrorMessage(createAttachment.error)}
                    </Alert>
                  </StackItem>
                ) : null}
              </Stack>
            </ModalBody>
            <ModalFooter>
              <Button
                variant="primary"
                onClick={submitForm}
                isDisabled={isSubmitting}
                isLoading={isSubmitting}
              >
                {t('Create')}
              </Button>
              <Button variant="link" onClick={onClose} isDisabled={isSubmitting}>
                {t('Cancel')}
              </Button>
            </ModalFooter>
          </Modal>
        );
      }}
    </Formik>
  );
};

export default ExternalIpAttachmentCreateModal;
