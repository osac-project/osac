import type { TFunction } from 'i18next';

import { DiskImageLifecycle, DiskImages } from '@osac/types';

import { useUpdateResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { getResourceLifecycleActions } from '../Resource/getResourceLifecycleActions';
import { useToast } from '../Toast/useToast';

export type DiskImageLifecycleAction = Exclude<DiskImageLifecycle, DiskImageLifecycle.UNSPECIFIED>;

const DISK_IMAGE_LIFECYCLE_ACTION_RULES = {
  canDeprecate: [DiskImageLifecycle.AVAILABLE],
  canObsolete: [DiskImageLifecycle.AVAILABLE, DiskImageLifecycle.DEPRECATED],
  canReactivate: [DiskImageLifecycle.DEPRECATED, DiskImageLifecycle.OBSOLETE],
  canDelete: [DiskImageLifecycle.OBSOLETE],
};

/** Determines which lifecycle transitions are valid from the disk image's current state. */
export const getDiskImageLifecycleActions = (state: DiskImageLifecycle | undefined) =>
  getResourceLifecycleActions(
    state,
    DiskImageLifecycle.UNSPECIFIED,
    DISK_IMAGE_LIFECYCLE_ACTION_RULES,
  );

const getLifecycleErrorTitle = (t: TFunction, action: DiskImageLifecycleAction): string => {
  switch (action) {
    case DiskImageLifecycle.DEPRECATED:
      return t('Failed to deprecate disk image');
    case DiskImageLifecycle.OBSOLETE:
      return t('Failed to mark disk image as obsolete');
    case DiskImageLifecycle.AVAILABLE:
      return t('Failed to reactivate disk image');
  }
};

/** Runs a disk image lifecycle transition and surfaces a toast if it fails. */
export const useDiskImageLifecycleAction = () => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const updateDiskImage = useUpdateResource(DiskImages);

  const runLifecycleAction = (diskImageId: string, action: DiskImageLifecycleAction) => {
    void updateDiskImage
      .mutateAsync({ object: { id: diskImageId, spec: { lifecycle: action } } })
      .catch((error) => {
        addToast({
          variant: 'danger',
          title: getLifecycleErrorTitle(t, action),
          description: getErrorMessage(error),
        });
      });
  };

  return { runLifecycleAction };
};
