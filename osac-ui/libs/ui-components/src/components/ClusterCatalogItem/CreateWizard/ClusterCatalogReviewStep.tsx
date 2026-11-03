import { Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';
import type { TFunction } from 'i18next';

import type { ClusterCatalogItem } from '@osac/types';

import type { ClusterCatalogItemValues } from './values';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import CatalogReview, {
  type CatalogReviewField,
  CatalogReviewRow,
  CatalogReviewSection,
  CatalogReviewValue,
  catalogReviewChanges,
  catalogReviewPolicyLabel,
  catalogReviewResourceKey,
  catalogReviewValue,
} from '../../CatalogItem/CatalogReview';
import { formatClusterNodeSetReview } from '../../Cluster/CreateWizard/clusterNodeSetReview';

interface ClusterCatalogReviewStepProps {
  item?: ClusterCatalogItem;
}

const nodeSetsKey = (values: ClusterCatalogItemValues['nodeSets']['value']) =>
  JSON.stringify(
    values.map((nodeSet) => ({
      hostType: nodeSet.hostType.name,
      name: nodeSet.name.trim(),
      size: nodeSet.size.trim(),
    })),
  );

const nodeSetsLabel = (
  t: TFunction,
  values: ClusterCatalogItemValues['nodeSets']['value'],
): string =>
  values.length
    ? values
        .map((nodeSet) => {
          const hostType = catalogReviewValue(t, nodeSet.hostType.name);
          const size = catalogReviewValue(t, nodeSet.size);
          return `${catalogReviewValue(t, nodeSet.name)}: ${formatClusterNodeSetReview(hostType, size)}`;
        })
        .join('; ')
    : t('Not set');

const catalogEditChanges = (
  t: TFunction,
  initialValues: ClusterCatalogItemValues,
  values: ClusterCatalogItemValues,
): ReturnType<typeof catalogReviewChanges> => {
  const fields: CatalogReviewField[] = [
    {
      label: t('Version'),
      before: catalogReviewValue(t, initialValues.version.value.name),
      after: catalogReviewValue(t, values.version.value.name),
      beforeKey: catalogReviewResourceKey(initialValues.version.value),
      afterKey: catalogReviewResourceKey(values.version.value),
    },
    {
      label: t('Version access'),
      before: catalogReviewPolicyLabel(t, initialValues.version),
      after: catalogReviewPolicyLabel(t, values.version),
      beforeKey: initialValues.version.locked,
      afterKey: values.version.locked,
    },
    {
      label: t('Node sets'),
      before: nodeSetsLabel(t, initialValues.nodeSets.value),
      after: nodeSetsLabel(t, values.nodeSets.value),
      beforeKey: nodeSetsKey(initialValues.nodeSets.value),
      afterKey: nodeSetsKey(values.nodeSets.value),
    },
    {
      label: t('Node sets access'),
      before: catalogReviewPolicyLabel(t, initialValues.nodeSets),
      after: catalogReviewPolicyLabel(t, values.nodeSets),
      beforeKey: initialValues.nodeSets.locked,
      afterKey: values.nodeSets.locked,
    },
    {
      label: t('Pod CIDR'),
      before: catalogReviewValue(t, initialValues.podCidr.value),
      after: catalogReviewValue(t, values.podCidr.value),
      beforeKey: initialValues.podCidr.value.trim(),
      afterKey: values.podCidr.value.trim(),
    },
    {
      label: t('Pod CIDR access'),
      before: catalogReviewPolicyLabel(t, initialValues.podCidr),
      after: catalogReviewPolicyLabel(t, values.podCidr),
      beforeKey: initialValues.podCidr.locked,
      afterKey: values.podCidr.locked,
    },
    {
      label: t('Service CIDR'),
      before: catalogReviewValue(t, initialValues.serviceCidr.value),
      after: catalogReviewValue(t, values.serviceCidr.value),
      beforeKey: initialValues.serviceCidr.value.trim(),
      afterKey: values.serviceCidr.value.trim(),
    },
    {
      label: t('Service CIDR access'),
      before: catalogReviewPolicyLabel(t, initialValues.serviceCidr),
      after: catalogReviewPolicyLabel(t, values.serviceCidr),
      beforeKey: initialValues.serviceCidr.locked,
      afterKey: values.serviceCidr.locked,
    },
  ];

  return catalogReviewChanges(fields);
};

const ClusterCatalogReviewStep = ({ item }: ClusterCatalogReviewStepProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const { initialValues, values } = useFormikContext<ClusterCatalogItemValues>();
  const isEdit = Boolean(item);
  return (
    <CatalogReview
      isEdit={isEdit}
      editChanges={isEdit ? catalogEditChanges(t, initialValues, values) : []}
    >
      <CatalogReviewSection title={t('Details')}>
        {role === 'admin' && (
          <CatalogReviewRow
            label={t('Scope')}
            value={
              values.scope === 'global'
                ? t('Global')
                : displayValue(values.metadata.tenant.name || '')
            }
          />
        )}
        <CatalogReviewRow label={t('Name')} value={displayValue(values.metadata.name)} />
        <CatalogReviewRow label={t('Description')} value={displayValue(values.description)} />
        <CatalogReviewRow
          label={t('Provisioning template')}
          value={displayValue(values.template.name || '')}
        />
        <CatalogReviewRow label={t('Status')} value={t('Unpublished')} />
      </CatalogReviewSection>

      <CatalogReviewSection title={t('Compute configuration')}>
        <CatalogReviewRow
          label={t('Version')}
          value={
            <CatalogReviewValue
              field={values.version}
              value={catalogReviewValue(t, values.version.value.name)}
            />
          }
        />
        <CatalogReviewRow
          label={t('Node sets')}
          value={
            <CatalogReviewValue
              field={values.nodeSets}
              value={
                values.nodeSets.value.length ? (
                  <Stack hasGutter={false}>
                    {values.nodeSets.value.map((nodeSet) => (
                      <StackItem key={nodeSet.rowId}>
                        {formatClusterNodeSetReview(
                          catalogReviewValue(t, nodeSet.hostType.name),
                          catalogReviewValue(t, nodeSet.size),
                        )}
                      </StackItem>
                    ))}
                  </Stack>
                ) : (
                  t('Not set')
                )
              }
            />
          }
        />
      </CatalogReviewSection>

      <CatalogReviewSection title={t('Networking')}>
        <CatalogReviewRow
          label={t('Pod CIDR')}
          value={
            <CatalogReviewValue
              field={values.podCidr}
              value={catalogReviewValue(t, values.podCidr.value)}
            />
          }
        />
        <CatalogReviewRow
          label={t('Service CIDR')}
          value={
            <CatalogReviewValue
              field={values.serviceCidr}
              value={catalogReviewValue(t, values.serviceCidr.value)}
            />
          }
        />
      </CatalogReviewSection>
    </CatalogReview>
  );
};

export default ClusterCatalogReviewStep;
