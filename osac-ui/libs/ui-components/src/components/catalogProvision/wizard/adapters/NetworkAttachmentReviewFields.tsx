import {
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import type { ResourceSelectValue } from '../../../Form/resourceSelectValue';

interface NetworkAttachmentReviewFieldsProps {
  virtualNetwork: ResourceSelectValue;
  subnet: ResourceSelectValue;
  securityGroups: ResourceSelectValue[];
}

/**
 * Shared review fields for VN / subnet / security-groups, reused by
 * VM, Cluster, and (in future) bare-metal review steps.
 *
 * Renders three `DescriptionListGroup` items — must be placed inside a
 * PatternFly `DescriptionList`.
 */
export const NetworkAttachmentReviewFields = ({
  virtualNetwork,
  subnet,
  securityGroups,
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
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
        <DescriptionListDescription>
          {securityGroups.length > 0
            ? securityGroups.map((sg) => sg.name || sg.id).join(', ')
            : '—'}
        </DescriptionListDescription>
      </DescriptionListGroup>
    </>
  );
};
