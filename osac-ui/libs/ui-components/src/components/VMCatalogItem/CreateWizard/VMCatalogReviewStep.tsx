import { Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';
import type { TFunction } from 'i18next';

import { GuestOSFamily } from '@osac/types';
import type { ComputeInstanceCatalogItem } from '@osac/types';

import type { ComputeCatalogItemValues } from './values';
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

interface VMCatalogReviewStepProps {
  item?: ComputeInstanceCatalogItem;
}

const resourceValueKey = (value: { id?: string; name?: string }) =>
  `${value.id?.trim() ?? ''}|${value.name?.trim() ?? ''}`;

const guestOsFamilyLabel = (t: TFunction, value: GuestOSFamily): string => {
  switch (value) {
    case GuestOSFamily.GUEST_OS_FAMILY_LINUX:
      return t('Linux');
    case GuestOSFamily.GUEST_OS_FAMILY_WINDOWS:
      return t('Microsoft Windows');
    case GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED:
    default:
      return t('Unspecified');
  }
};

const bootDiskSizeLabel = (t: TFunction, value: string): string =>
  value.trim() ? `${value.trim()} GiB` : t('Not set');

const additionalDisksKey = (values: ComputeCatalogItemValues['additionalDisks']['value']) =>
  JSON.stringify(
    values.map((disk) => ({
      sizeGib: disk.sizeGib.trim(),
      storageTier: resourceValueKey(disk.storageTier),
    })),
  );

const additionalDisksLabel = (
  t: TFunction,
  values: ComputeCatalogItemValues['additionalDisks']['value'],
): string =>
  values.length
    ? values
        .map((disk, index) => {
          const size = disk.sizeGib.trim() ? `${disk.sizeGib.trim()} GiB` : t('Not set');
          const tier = catalogReviewResourceLabel(t, disk.storageTier);
          return `${t('Disk set {{number}}', { number: index + 1 })}: ${size} · ${tier}`;
        })
        .join('; ')
    : t('Not set');

const userDataKey = (values: ComputeCatalogItemValues) =>
  JSON.stringify({
    locked: values.userData.locked,
    value: values.userData.value,
    source: values.userData.value.trim()
      ? 'inline'
      : values.userDataSecret.value.trim()
        ? 'secret'
        : 'none',
  });

const userDataLabel = (t: TFunction, values: ComputeCatalogItemValues): string => {
  return values.userData.value.trim() || values.userDataSecret.value.trim()
    ? t('Configured')
    : t('Not set');
};

const catalogEditChanges = (
  t: TFunction,
  initialValues: ComputeCatalogItemValues,
  values: ComputeCatalogItemValues,
): ReturnType<typeof catalogReviewChanges> => {
  const fields: CatalogReviewField[] = [
    {
      label: t('Guest operating system'),
      before: guestOsFamilyLabel(t, initialValues.guestOsFamily),
      after: guestOsFamilyLabel(t, values.guestOsFamily),
      beforeKey: initialValues.guestOsFamily,
      afterKey: values.guestOsFamily,
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
      label: t('Boot disk size'),
      before: bootDiskSizeLabel(t, initialValues.bootDiskSizeGib.value),
      after: bootDiskSizeLabel(t, values.bootDiskSizeGib.value),
      beforeKey: initialValues.bootDiskSizeGib.value.trim(),
      afterKey: values.bootDiskSizeGib.value.trim(),
    },
    {
      label: t('Boot disk size access'),
      before: catalogReviewPolicyLabel(t, initialValues.bootDiskSizeGib),
      after: catalogReviewPolicyLabel(t, values.bootDiskSizeGib),
      beforeKey: initialValues.bootDiskSizeGib.locked,
      afterKey: values.bootDiskSizeGib.locked,
    },
    {
      label: t('Boot storage tier'),
      before: catalogReviewResourceLabel(t, initialValues.bootDiskStorageTier.value),
      after: catalogReviewResourceLabel(t, values.bootDiskStorageTier.value),
      beforeKey: resourceValueKey(initialValues.bootDiskStorageTier.value),
      afterKey: resourceValueKey(values.bootDiskStorageTier.value),
    },
    {
      label: t('Boot storage tier access'),
      before: catalogReviewPolicyLabel(t, initialValues.bootDiskStorageTier),
      after: catalogReviewPolicyLabel(t, values.bootDiskStorageTier),
      beforeKey: initialValues.bootDiskStorageTier.locked,
      afterKey: values.bootDiskStorageTier.locked,
    },
    {
      label: t('Additional disks'),
      before: additionalDisksLabel(t, initialValues.additionalDisks.value),
      after: additionalDisksLabel(t, values.additionalDisks.value),
      beforeKey: additionalDisksKey(initialValues.additionalDisks.value),
      afterKey: additionalDisksKey(values.additionalDisks.value),
    },
    {
      label: t('Additional disks access'),
      before: catalogReviewPolicyLabel(t, initialValues.additionalDisks),
      after: catalogReviewPolicyLabel(t, values.additionalDisks),
      beforeKey: initialValues.additionalDisks.locked,
      afterKey: values.additionalDisks.locked,
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

const VMCatalogReviewStep = ({ item }: VMCatalogReviewStepProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const { initialValues, values } = useFormikContext<ComputeCatalogItemValues>();
  const isEdit = Boolean(item);
  const editChanges = isEdit ? catalogEditChanges(t, initialValues, values) : [];
  const notSet = t('Not set');
  const diskImage = catalogReviewResourceLabel(t, values.diskImage.value);
  const instanceTypeLabel = catalogReviewResourceLabel(t, values.instanceType.value);
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
        <CatalogReviewRow
          label={t('Provisioning template')}
          value={displayValue(values.template.name || '')}
        />
        <CatalogReviewRow label={t('Status')} value={t('Unpublished')} />
      </CatalogReviewSection>

      <CatalogReviewSection title={t('Compute configuration')}>
        <CatalogReviewRow
          label={t('Guest operating system')}
          value={guestOsFamilyLabel(t, values.guestOsFamily)}
        />
        <CatalogReviewRow
          label={t('Disk image')}
          value={<CatalogReviewValue field={values.diskImage} value={diskImage} />}
        />
        <CatalogReviewRow
          label={t('Instance type')}
          value={<CatalogReviewValue field={values.instanceType} value={instanceTypeLabel} />}
        />
      </CatalogReviewSection>

      <CatalogReviewSection title={t('Storage')}>
        <CatalogReviewRow
          label={t('Boot disk size')}
          value={
            <CatalogReviewValue
              field={values.bootDiskSizeGib}
              value={bootDiskSizeLabel(t, values.bootDiskSizeGib.value)}
            />
          }
        />
        <CatalogReviewRow
          label={t('Boot storage tier')}
          value={
            <CatalogReviewValue
              field={values.bootDiskStorageTier}
              value={catalogReviewResourceLabel(t, values.bootDiskStorageTier.value)}
            />
          }
        />
        <CatalogReviewRow
          label={t('Additional disks')}
          value={
            values.additionalDisks.value.length ? (
              <CatalogReviewValue
                field={values.additionalDisks}
                value={
                  <Stack hasGutter={false}>
                    {values.additionalDisks.value.map((disk, index) => (
                      <StackItem key={`${disk.sizeGib}-${index}`}>
                        {`${t('Disk set {{number}}', { number: index + 1 })}: ${
                          disk.sizeGib.trim() ? `${disk.sizeGib.trim()} GiB` : notSet
                        } · ${catalogReviewResourceLabel(t, disk.storageTier)}`}
                      </StackItem>
                    ))}
                  </Stack>
                }
              />
            ) : (
              notSet
            )
          }
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

export default VMCatalogReviewStep;
