import AutoExternalIpField from './AutoExternalIpField';
import NetworkAttachmentFields from './NetworkAttachmentFields';
import ProvisioningField from './ProvisioningField';

interface NetworkFieldsProps {
  networkAttachmentName: string;
  externalIpAttachmentName: string;
  isDisabled?: boolean;
  requireVmSupport?: boolean;
  autoExternalIpHelperText?: string;
}

const NetworkFields = ({
  networkAttachmentName,
  externalIpAttachmentName,
  isDisabled = false,
  requireVmSupport = false,
  autoExternalIpHelperText,
}: NetworkFieldsProps) => (
  <>
    <ProvisioningField name={networkAttachmentName}>
      {({ valueName, isDisabled: fieldIsDisabled }) => (
        <NetworkAttachmentFields
          name={valueName}
          isDisabled={isDisabled || fieldIsDisabled}
          requireVmSupport={requireVmSupport}
        />
      )}
    </ProvisioningField>
    <ProvisioningField name={externalIpAttachmentName}>
      {({ valueName, isDisabled: fieldIsDisabled }) => (
        <AutoExternalIpField
          name={valueName}
          isDisabled={isDisabled || fieldIsDisabled}
          helperText={autoExternalIpHelperText}
        />
      )}
    </ProvisioningField>
  </>
);

export default NetworkFields;
