import {
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';
import type { ResourceSelectValue } from '../Form/resourceSelectValue';

interface NetworkAttachmentReviewFieldsProps {
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
  showSecurityGroups?: boolean;
}

/**
 * Shared review fields for virtual network, subnet, and optional security groups,
 * reused by VM, cluster, and bare-metal review steps.
 *
 * Place inside a PatternFly `DescriptionList`.
 */
export const NetworkAttachmentReviewFields = ({
  virtualNetwork,
  subnet,
  securityGroups,
  showSecurityGroups = true,
}: NetworkAttachmentReviewFieldsProps) => {
  const { t } = useTranslation();

  return (
    <>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Virtual network')}</DescriptionListTerm>
        <DescriptionListDescription>
          {virtualNetwork.name || virtualNetwork.id || '—'}
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Subnet')}</DescriptionListTerm>
        <DescriptionListDescription>{subnet.name || subnet.id || '—'}</DescriptionListDescription>
      </DescriptionListGroup>
      {showSecurityGroups && (
        <DescriptionListGroup>
          <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
          <DescriptionListDescription>
            {securityGroups.length > 0
              ? securityGroups.map((sg) => sg.name || sg.id).join(', ')
              : '—'}
          </DescriptionListDescription>
        </DescriptionListGroup>
      )}
    </>
  );
};
