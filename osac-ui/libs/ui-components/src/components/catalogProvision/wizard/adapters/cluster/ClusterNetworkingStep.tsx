import { useEffect, useMemo, useRef } from 'react';
import { FormSection, Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { ClusterCatalogItem } from '@osac/types';

import type { ClusterWizardValues } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { CheckboxField } from '../../../../Form/CheckboxField';
import { InputField } from '../../../../Form/InputField';
import { NetworkAttachmentPickers } from '../../../../Form/NetworkAttachmentPickers';
import OsacForm from '../../../../Form/OsacForm';
import { getCatalogFieldOverlay, readCatalogFieldDefinitions } from '../../catalogOverlay';
import { useWizardValidation } from '../../WizardValidationContext';

interface Props {
  catalogItem: ClusterCatalogItem | null;
}

export const ClusterNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { clearValidationAlert } = useWizardValidation();
  const { values, setFieldTouched, validateForm } = useFormikContext<ClusterWizardValues>();

  const useDefaultNetwork = values.spec.useDefaultNetwork;

  const definitions = useMemo(() => readCatalogFieldDefinitions(catalogItem), [catalogItem]);
  const podCidrOverlay = useMemo(
    () => getCatalogFieldOverlay('network.pod_cidr', definitions, t('Pod CIDR')),
    [definitions, t],
  );
  const serviceCidrOverlay = useMemo(
    () => getCatalogFieldOverlay('network.service_cidr', definitions, t('Service CIDR')),
    [definitions, t],
  );

  // Clear validation alerts when toggling back to defaults
  const previousUseDefaultRef = useRef(useDefaultNetwork);
  useEffect(() => {
    const wasCustom = previousUseDefaultRef.current === false;
    previousUseDefaultRef.current = useDefaultNetwork;

    if (!useDefaultNetwork || !wasCustom) {
      return;
    }

    void setFieldTouched('spec.networkAttachment.virtualNetwork', false, false);
    void setFieldTouched('spec.networkAttachment.subnet', false, false);
    void setFieldTouched('spec.networkAttachment.securityGroups', false, false);
    clearValidationAlert();
    void validateForm();
  }, [clearValidationAlert, setFieldTouched, useDefaultNetwork, validateForm]);

  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <FormSection title={t('Infrastructure Networking')} titleElement="h2">
            <CheckboxField
              name="spec.useDefaultNetwork"
              label={t('Use tenant default network')}
              fieldId="cluster-use-default-network"
            />
            {!useDefaultNetwork && (
              <NetworkAttachmentPickers
                fieldPrefix="spec.networkAttachment"
                fieldIdPrefix="cluster"
              />
            )}
            <CheckboxField
              name="spec.autoExternalIpAttachment"
              label={t('Auto External IP Attachment')}
              fieldId="cluster-auto-external-ip"
              helperText={t(
                'Automatically provision external IPs for the cluster API and ingress endpoints.',
              )}
            />
          </FormSection>
          <FormSection title={t('Cluster Networking')} titleElement="h2">
            <InputField
              name="spec.network.podCidr"
              label={podCidrOverlay.label}
              fieldId="cluster-pod-cidr"
              isDisabled={!podCidrOverlay.editable}
              helperText={t('Use IPv4 CIDR notation (for example 10.128.0.0/14).')}
            />
            <InputField
              name="spec.network.serviceCidr"
              label={serviceCidrOverlay.label}
              fieldId="cluster-service-cidr"
              isDisabled={!serviceCidrOverlay.editable}
              helperText={t('Use IPv4 CIDR notation (for example 172.30.0.0/16).')}
            />
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
