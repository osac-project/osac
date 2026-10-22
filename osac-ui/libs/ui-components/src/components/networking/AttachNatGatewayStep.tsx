import { Alert, Content, FormGroup, Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type {
  AttachNatGatewayExternalIpOption,
  AttachNatGatewayFormValues,
  AttachNatGatewayVirtualNetwork,
} from './AttachNatGatewayWizard.types';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import NameField from '../Form/NameField';
import OsacForm from '../Form/OsacForm';
import { SelectField } from '../Form/SelectField';

export interface AttachNatGatewayStepProps {
  virtualNetwork: AttachNatGatewayVirtualNetwork;
  isLoadingExternalIps: boolean;
  externalIpsError: unknown;
  natGatewayError: unknown;
  noExternalIpsAvailable: boolean;
  hasNatGateway: boolean;
  externalIpOptions: AttachNatGatewayExternalIpOption[];
}

const AttachNatGatewayStep = ({
  virtualNetwork,
  isLoadingExternalIps,
  externalIpsError,
  natGatewayError,
  noExternalIpsAvailable,
  hasNatGateway,
  externalIpOptions,
}: AttachNatGatewayStepProps) => {
  const { t } = useTranslation();
  const { isSubmitting } = useFormikContext<AttachNatGatewayFormValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Content component="p">
          {t('Provides outbound internet access for workloads in this virtual network.')}
        </Content>
      </StackItem>
      {noExternalIpsAvailable && (
        <StackItem>
          <Alert variant="warning" title={t('No unallocated external IPs')} isInline>
            {t('Allocate an External IP that is not in use, or contact your administrator.')}
          </Alert>
        </StackItem>
      )}
      {hasNatGateway && (
        <StackItem>
          <Alert variant="warning" title={t('NAT gateway already attached')} isInline>
            {t('This virtual network already has a NAT gateway attached.')}
          </Alert>
        </StackItem>
      )}
      {!!externalIpsError && (
        <StackItem>
          <Alert variant="danger" title={t('Error loading external IPs')} isInline>
            {getErrorMessage(externalIpsError)}
          </Alert>
        </StackItem>
      )}
      {!!natGatewayError && (
        <StackItem>
          <Alert variant="danger" title={t('Error loading NAT gateways')} isInline>
            {getErrorMessage(natGatewayError)}
          </Alert>
        </StackItem>
      )}
      <StackItem>
        <OsacForm>
          <FormGroup label={t('Virtual network')} fieldId="attach-nat-gateway-network">
            {virtualNetwork.metadata?.name ?? virtualNetwork.id}
          </FormGroup>
          <FormGroup label={t('IPv4 CIDR')} fieldId="attach-nat-gateway-cidr">
            <code>{virtualNetwork.spec?.ipv4Cidr ?? '—'}</code>
          </FormGroup>
          <NameField isDisabled={isSubmitting} />
          <SelectField
            name="externalIpId"
            label={t('External IP')}
            fieldId="attach-nat-gateway-external-ip"
            isRequired
            isLoading={isLoadingExternalIps}
            isDisabled={noExternalIpsAvailable || Boolean(externalIpsError)}
            placeholder={t('Select an external IP')}
            helperText={t('Standard edge NAT for outbound internet access.')}
            options={externalIpOptions}
            autoSelectSingleOption
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default AttachNatGatewayStep;
