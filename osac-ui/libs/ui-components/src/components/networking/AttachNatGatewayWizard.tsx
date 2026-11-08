import { useMemo, useState } from 'react';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';
import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { ExternalIPs, NATGateways } from '@osac/types';

import AttachNatGatewayReviewStep from './AttachNatGatewayReviewStep';
import AttachNatGatewayStep from './AttachNatGatewayStep';
import type {
  AttachNatGatewayFormValues,
  AttachNatGatewayVirtualNetwork,
} from './AttachNatGatewayWizard.types';
import {
  useCreateResource,
  useInvalidateServiceQueries,
  useListAllResources,
} from '../../api/use-resource';
import { useExternalIpConsumers } from '../../api/v1/external-ip-data';
import { unallocatedExternalIpFilter } from '../../api/v1/networking';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import { resourceNameSchema } from '../../validation/resource-name';
import { FieldValidationProvider } from '../Form/FieldValidationContext';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';
import { OSACWizardFooter } from '../Wizard/OSACWizardFooter';

export interface AttachNatGatewayWizardProps {
  virtualNetwork: AttachNatGatewayVirtualNetwork;
  /** Breadcrumb links before "Create NAT gateway"; the page the flow started from. */
  breadcrumbParents: { to: string; label: string }[];
  /** Called after Create and on Cancel; returns to where the flow started. */
  onClose: () => void;
}

export const attachNatGatewayStepHasErrors = (
  stepId: string,
  errors: FormikErrors<AttachNatGatewayFormValues>,
): boolean => {
  if (stepId !== 'nat-gateway') {
    return false;
  }

  return Boolean(errors.metadata?.name || errors.externalIpId);
};

const validationSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    externalIpId: Yup.string().required(t('An external IP is required')),
  });

export const AttachNatGatewayWizard = ({
  virtualNetwork,
  breadcrumbParents,
  onClose,
}: AttachNatGatewayWizardProps) => {
  const { t } = useTranslation();
  const [currentStep, setCurrentStep] = useState('nat-gateway');
  const { role } = useSession();
  const invalidateService = useInvalidateServiceQueries();
  const {
    data: externalIpsReply,
    isLoading: isLoadingExternalIps,
    error: externalIpsError,
    // Every page: the consumer and tenant checks below run in the browser.
  } = useListAllResources(ExternalIPs, unallocatedExternalIpFilter());
  const externalIps = externalIpsReply?.items;
  const {
    natGateways,
    isSelectable,
    isLoading: isLoadingNatGateways,
    error: natGatewayError,
  } = useExternalIpConsumers();
  const createNatGateway = useCreateResource(NATGateways, {
    onSuccess: async () => {
      await invalidateService(ExternalIPs);
    },
  });
  const hasNatGateway = natGateways.some(
    (gateway) => gateway.spec?.virtualNetwork?.id === virtualNetwork.id,
  );

  const externalIpOptions = useMemo(
    () =>
      (externalIps ?? [])
        // A provider admin sees every tenant's external IPs; the server accepts only the
        // virtual network's tenant.
        .filter(
          (ip) =>
            isSelectable(ip) &&
            (role !== 'admin' ||
              !virtualNetwork.metadata?.tenant ||
              ip.metadata?.tenant === virtualNetwork.metadata.tenant),
        )
        .map((ip) => ({
          value: ip.id,
          label: `${ip.metadata?.name ?? ip.id} · ${ip.status?.address ?? '—'}`,
        })),
    [externalIps, isSelectable, role, virtualNetwork.metadata?.tenant],
  );

  const noExternalIpsAvailable =
    !isLoadingExternalIps && !externalIpsError && externalIpOptions.length === 0;

  const handleClose = () => {
    setCurrentStep('nat-gateway');
    onClose();
  };

  return (
    <Formik<AttachNatGatewayFormValues>
      initialValues={{
        metadata: { name: '' },
        externalIpId: '',
      }}
      validationSchema={validationSchema(t)}
      onSubmit={async (values) => {
        if (hasNatGateway) {
          return;
        }
        try {
          await createNatGateway.mutateAsync({
            object: {
              metadata: {
                name: values.metadata.name,
                // The NAT gateway lives in the virtual network's project (and, for a
                // provider admin, its tenant).
                project: virtualNetwork.metadata?.project,
                ...(role === 'admin' && virtualNetwork.metadata?.tenant
                  ? { tenant: virtualNetwork.metadata.tenant }
                  : {}),
              },
              spec: {
                virtualNetwork: { id: virtualNetwork.id },
                externalIp: { id: values.externalIpId },
              },
            },
          });
          onClose();
        } catch {
          // surfaced via the mutation error state
        }
      }}
    >
      {() => (
        <FieldValidationProvider>
          <PageSection hasBodyWrapper={false}>
            <ResourceDetailHeader
              parents={breadcrumbParents}
              resourceName={t('Create NAT gateway')}
            />
          </PageSection>
          <PageSection
            hasBodyWrapper={false}
            isFilled
            type={PageSectionTypes.wizard}
            aria-label={t('Create NAT gateway wizard')}
          >
            <Wizard
              navAriaLabel={t('Create NAT gateway steps')}
              isVisitRequired
              footer={
                <OSACWizardFooter
                  onCancel={handleClose}
                  stepHasErrors={attachNatGatewayStepHasErrors}
                  error={createNatGateway.error}
                  isNextDisabled={
                    isLoadingNatGateways ||
                    Boolean(natGatewayError) ||
                    hasNatGateway ||
                    noExternalIpsAvailable ||
                    Boolean(externalIpsError)
                  }
                  submitLabel={t('Create')}
                  errorTitle={t('Failed to create NAT gateway')}
                />
              }
              onStepChange={(_, step) => setCurrentStep(step.id as string)}
            >
              <WizardStep id="nat-gateway" name={t('NAT gateway')}>
                {currentStep === 'nat-gateway' && (
                  <AttachNatGatewayStep
                    virtualNetwork={virtualNetwork}
                    isLoadingExternalIps={isLoadingExternalIps}
                    externalIpsError={externalIpsError}
                    natGatewayError={natGatewayError}
                    noExternalIpsAvailable={noExternalIpsAvailable}
                    hasNatGateway={hasNatGateway}
                    externalIpOptions={externalIpOptions}
                  />
                )}
              </WizardStep>
              <WizardStep id="review" name={t('Review')}>
                {currentStep === 'review' && (
                  <AttachNatGatewayReviewStep
                    virtualNetwork={virtualNetwork}
                    externalIpOptions={externalIpOptions}
                  />
                )}
              </WizardStep>
            </Wizard>
          </PageSection>
        </FieldValidationProvider>
      )}
    </Formik>
  );
};
