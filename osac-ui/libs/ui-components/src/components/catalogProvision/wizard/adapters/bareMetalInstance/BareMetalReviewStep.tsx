import {
  Alert,
  Bullseye,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Spinner,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { BareMetalInstanceType, BareMetalInstanceTypes } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useProjects } from '@osac/ui-components/api/v1/project';
import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';
import {
  fullProjectPathToQueryFilter,
  getProjectName,
} from '@osac/ui-components/components/Project/utils';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { type BareMetalInstanceWizardValues, hasBareMetalAuthentication } from './fields';
import {
  formatResourceIdsForReview,
  resourceDisplayName,
  useSecurityGroups,
  useSubnets,
  useVirtualNetworks,
} from '../../../../../api/v1/networking';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { formatReviewScalar } from '../../catalogOverlay';

interface Props {
  catalogItem: CatalogItem | null;
}

export const BareMetalReviewStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();
  const hasAuthentication = hasBareMetalAuthentication(values.spec.sshKey, values.spec.userData);

  const { data, isLoading, error } = useProjects({
    filter: fullProjectPathToQueryFilter(values.metadata.project),
  });

  const {
    data: instanceTypes,
    isLoading: instanceTypesLoading,
    error: instanceTypeError,
  } = useListResource(BareMetalInstanceTypes, {
    filter: cel<BareMetalInstanceType>((filter) =>
      filter.field('metadata.name').equals(values.spec.instanceType.name),
    ),
  });

  // Fetch networking resources for review display
  const { data: virtualNetworks = [] } = useVirtualNetworks();
  const { data: subnets = [] } = useSubnets();
  const { data: securityGroups = [] } = useSecurityGroups();

  if (isLoading || instanceTypesLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  const instanceType = instanceTypes?.items.length ? instanceTypes.items[0] : undefined;

  // Format networking summary
  const networking = values.spec.networking;
  const networkingSummary = networking.useDefaults
    ? t('Using tenant default network')
    : networking.attachments
        .slice(0, 1)
        .map((attachment) => {
          const vn = virtualNetworks.find((v) => v.id === attachment.virtualNetwork);
          const subnet = subnets.find((s) => s.id === attachment.subnet);
          const sgNames = formatResourceIdsForReview(attachment.securityGroups, securityGroups);
          return `${resourceDisplayName(vn?.metadata, vn?.id)} / ${resourceDisplayName(subnet?.metadata, subnet?.id)} / ${sgNames}`;
        })
        .join('\n');

  return (
    <Stack hasGutter>
      {!hasAuthentication && (
        <StackItem>
          <Alert
            variant="danger"
            isInline
            title={t(
              'Provide either an SSH public key or user data containing access credentials.',
            )}
          />
        </StackItem>
      )}
      {!!error && (
        <StackItem>
          <Alert variant="warning" isInline title={t('Failed to fetch project')}>
            {getErrorMessage(error)}
          </Alert>
        </StackItem>
      )}
      {!!instanceTypeError && (
        <StackItem>
          <Alert variant="warning" isInline title={t('Failed to fetch instance type')}>
            {getErrorMessage(instanceTypeError)}
          </Alert>
        </StackItem>
      )}
      <StackItem>
        <DescriptionList
          isHorizontal
          isCompact
          aria-label={t('catalogProvision.steps.review.title')}
        >
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {catalogItem?.title || catalogItem?.metadata?.name || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {data?.length ? getProjectName(data[0], t) : '-'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.metadata.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('SSH public key')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.sshKey)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
            <DescriptionListDescription>{'-'}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              {instanceType
                ? `${instanceType.metadata?.name}${instanceType.spec?.description ? `(${instanceType.spec?.description})` : ''}`
                : values.spec.instanceType.name || '-'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('User data')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatReviewScalar(values.spec.userData, true)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Networking')}</DescriptionListTerm>
            <DescriptionListDescription style={{ whiteSpace: 'pre-line' }}>
              {networkingSummary}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('External access')}</DescriptionListTerm>
            <DescriptionListDescription>
              {networking.attachExternalIp ? t('Enabled') : t('Disabled')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};
