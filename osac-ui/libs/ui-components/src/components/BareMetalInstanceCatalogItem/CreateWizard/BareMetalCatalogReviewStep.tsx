import { useFormikContext } from 'formik';
import type { TFunction } from 'i18next';

import type { BareMetalInstanceCatalogItem } from '@osac/types';

import type { BareMetalCatalogItemValues } from './values';
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
  catalogReviewResourceLabel,
} from '../../CatalogItem/CatalogReview';

interface BareMetalCatalogReviewStepProps {
  item?: BareMetalInstanceCatalogItem;
}

const resourceValueKey = (value: { id?: string; name?: string }) =>
  `${value.id?.trim() ?? ''}|${value.name?.trim() ?? ''}`;

const userDataKey = (values: BareMetalCatalogItemValues) =>
  JSON.stringify({
    inline: values.userData.value,
    locked: values.userData.locked,
    secret: values.userDataSecret.value,
  });

const userDataLabel = (t: TFunction, values: BareMetalCatalogItemValues): string =>
  values.userData.value.trim() || values.userDataSecret.value.trim()
    ? t('Configured')
    : t('Not set');

const catalogEditChanges = (
  t: TFunction,
  initialValues: BareMetalCatalogItemValues,
  values: BareMetalCatalogItemValues,
): ReturnType<typeof catalogReviewChanges> => {
  const fields: CatalogReviewField[] = [
    {
      label: t('Instance type'),
      before: catalogReviewResourceLabel(t, initialValues.instanceType.value),
      after: catalogReviewResourceLabel(t, values.instanceType.value),
      beforeKey: resourceValueKey(initialValues.instanceType.value),
      afterKey: resourceValueKey(values.instanceType.value),
    },
    {
      label: t('Instance type access'),
      before: catalogReviewPolicyLabel(t, initialValues.instanceType),
      after: catalogReviewPolicyLabel(t, values.instanceType),
      beforeKey: initialValues.instanceType.locked,
      afterKey: values.instanceType.locked,
    },
    {
      label: t('Disk image'),
      before: catalogReviewResourceLabel(t, initialValues.diskImage.value),
      after: catalogReviewResourceLabel(t, values.diskImage.value),
      beforeKey: resourceValueKey(initialValues.diskImage.value),
      afterKey: resourceValueKey(values.diskImage.value),
    },
    {
      label: t('Disk image access'),
      before: catalogReviewPolicyLabel(t, initialValues.diskImage),
      after: catalogReviewPolicyLabel(t, values.diskImage),
      beforeKey: initialValues.diskImage.locked,
      afterKey: values.diskImage.locked,
    },
    {
      label: t('Cloud-init'),
      before: userDataLabel(t, initialValues),
      after: userDataLabel(t, values),
      beforeKey: userDataKey(initialValues),
      afterKey: userDataKey(values),
    },
    {
      label: t('Cloud-init access'),
      before: catalogReviewPolicyLabel(t, initialValues.userData),
      after: catalogReviewPolicyLabel(t, values.userData),
      beforeKey: initialValues.userData.locked,
      afterKey: values.userData.locked,
    },
  ];

  return catalogReviewChanges(fields);
};

const BareMetalCatalogReviewStep = ({ item }: BareMetalCatalogReviewStepProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const { initialValues, values } = useFormikContext<BareMetalCatalogItemValues>();
  const isEdit = Boolean(item);
  const editChanges = isEdit ? catalogEditChanges(t, initialValues, values) : [];
  const instanceType = catalogReviewResourceLabel(t, values.instanceType.value);
  const diskImage = catalogReviewResourceLabel(t, values.diskImage.value);
  const userDataReview = userDataLabel(t, values);

  return (
    <CatalogReview isEdit={isEdit} editChanges={editChanges}>
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
          label={t('Instance type')}
          value={<CatalogReviewValue field={values.instanceType} value={instanceType} />}
        />
        <CatalogReviewRow
          label={t('Disk image')}
          value={<CatalogReviewValue field={values.diskImage} value={diskImage} />}
        />
      </CatalogReviewSection>

      <CatalogReviewSection title={t('Access & Initial run')}>
        <CatalogReviewRow
          label={t('Cloud-init')}
          value={<CatalogReviewValue field={values.userData} value={userDataReview} />}
        />
      </CatalogReviewSection>
    </CatalogReview>
  );
};

export default BareMetalCatalogReviewStep;
