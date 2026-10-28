import { Content, Divider } from '@patternfly/react-core';

import AutoExternalIpField from './AutoExternalIpField';
import NetworkAttachmentFields from './NetworkAttachmentFields';
import ProvisioningField from './ProvisioningField';
import { useTranslation } from '../../../hooks/useTranslation';

interface NetworkFieldsProps {
  networkAttachmentName: string;
  externalIpAttachmentName: string;
  isDisabled?: boolean;
  autoExternalIpHelperText?: string;
  showSecurityGroups?: boolean;
}

const NetworkFields = ({
  networkAttachmentName,
  externalIpAttachmentName,
  isDisabled = false,
  autoExternalIpHelperText,
  showSecurityGroups = true,
}: NetworkFieldsProps) => {
  const { t } = useTranslation();

  return (
    <>
      <Content component="p">
        {t('Choose a network and whether to auto-attach an external IP.')}
      </Content>
      <ProvisioningField name={networkAttachmentName}>
        {({ valueName, isDisabled: fieldIsDisabled }) => (
          <NetworkAttachmentFields
            name={valueName}
            isDisabled={isDisabled || fieldIsDisabled}
            showSecurityGroups={showSecurityGroups}
          />
        )}
      </ProvisioningField>
      <Divider />
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
};

export default NetworkFields;
