import { useState } from 'react';
import { Alert, Button } from '@patternfly/react-core';
import { useField, useFormikContext } from 'formik';

import { VirtualNetworks } from '@osac/types';

import DefaultNetworkSummary from './DefaultNetworkSummary';
import { getEmptyNetworkAttachmentFormFields } from './networkAttachments';
import type { NetworkAttachmentFormFields } from './networkAttachments';
import NetworkSelectionCards from './NetworkSelectionCards';
import PopulateVirtualNetworkFromSubnet from './PopulateVirtualNetworkFromSubnet';
import SecurityGroupsMultiSelectField from './SecurityGroupsMultiSelectField';
import SubnetField from './SubnetField';
import VirtualNetworkField from './VirtualNetworkField';
import { useInvalidateServiceQueries } from '../../../api/use-resource';
import { usePermissions } from '../../../hooks/use-permissions';
import { useTranslation } from '../../../hooks/useTranslation';
import { VirtualNetworkCreateModal } from '../../networking/VirtualNetworkCreateModal';

import './NetworkAttachmentFields.css';

interface NetworkWizardValues {
  metadata: { project: string };
}

interface NetworkAttachmentFieldsProps {
  name: string;
  isDisabled?: boolean;
  showSecurityGroups?: boolean;
}

const NetworkAttachmentFields = ({
  name,
  isDisabled = false,
  showSecurityGroups = true,
}: NetworkAttachmentFieldsProps) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<NetworkWizardValues>();
  const [field, , helpers] = useField<NetworkAttachmentFormFields>(name);
  const { allowed: canCreateVirtualNetwork } = usePermissions(VirtualNetworks, 'create');
  const invalidateServiceQueries = useInvalidateServiceQueries();
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);
  const [createdNetworkName, setCreatedNetworkName] = useState('');
  const useDefaultNetwork = field.value.useDefaultNetwork;

  const selectNetwork = (useDefault: boolean) => {
    if (isDisabled || useDefault === useDefaultNetwork) {
      return;
    }
    void helpers.setValue(
      useDefault
        ? getEmptyNetworkAttachmentFormFields()
        : { ...field.value, useDefaultNetwork: false },
    );
    setCreatedNetworkName('');
  };

  return (
    <>
      <NetworkSelectionCards
        fieldName={name}
        useDefaultNetwork={useDefaultNetwork}
        isDisabled={isDisabled}
        showSecurityGroups={showSecurityGroups}
        onSelect={selectNetwork}
      />
      <div className="network-attachment-fields__details">
        {useDefaultNetwork ? (
          <DefaultNetworkSummary
            project={values.metadata.project}
            showSecurityGroups={showSecurityGroups}
          />
        ) : (
          <>
            {createdNetworkName && (
              <Alert variant="info" isInline title={t('Virtual network created')}>
                {t('{{name}} will appear in the list when it is ready.', {
                  name: createdNetworkName,
                })}
                <Button
                  variant="link"
                  isInline
                  onClick={() => void invalidateServiceQueries(VirtualNetworks)}
                >
                  {t('Refresh virtual networks')}
                </Button>
              </Alert>
            )}
            <PopulateVirtualNetworkFromSubnet
              virtualNetworkName={`${name}.virtualNetwork`}
              subnetName={`${name}.subnet`}
            />
            <VirtualNetworkField
              name={`${name}.virtualNetwork`}
              subnetName={`${name}.subnet`}
              securityGroupsName={`${name}.securityGroups`}
              showDescription
              onCreate={canCreateVirtualNetwork ? () => setIsCreateModalOpen(true) : undefined}
              isRequired
              isDisabled={isDisabled}
            />
            <SubnetField
              name={`${name}.subnet`}
              virtualNetworkName={`${name}.virtualNetwork`}
              isRequired
              showCidr
              isDisabled={isDisabled}
            />
            {showSecurityGroups && (
              <SecurityGroupsMultiSelectField
                name={`${name}.securityGroups`}
                virtualNetworkName={`${name}.virtualNetwork`}
                isDisabled={isDisabled}
              />
            )}
          </>
        )}
      </div>
      {isCreateModalOpen && (
        <VirtualNetworkCreateModal
          initialProject={values.metadata.project}
          onClose={() => setIsCreateModalOpen(false)}
          onCreated={(network) => {
            setIsCreateModalOpen(false);
            setCreatedNetworkName(network.metadata?.name || network.id);
            void invalidateServiceQueries(VirtualNetworks);
          }}
        />
      )}
    </>
  );
};

export default NetworkAttachmentFields;
