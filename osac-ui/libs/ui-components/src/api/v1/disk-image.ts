import { type MessageInitShape } from '@bufbuild/protobuf';
import { keepPreviousData, useMutation } from '@tanstack/react-query';

import {
  Architecture,
  type DiskImage,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImages,
  GuestOSFamily,
} from '@osac/types';

import { useApiFetch } from '../api-context';
import { type CelFilter, cel } from '../cel';
import { type ListParams, apiQueryKey } from '../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';
import { buildUpdateMaskPaths } from './update-mask';

type DiskImagesQueryOptions = {
  enabled?: boolean;
};

type DiskImageListParams = Omit<ListParams, 'filter'> & {
  filter?: CelFilter<DiskImage>;
};

export const DISK_IMAGE_NON_OBSOLETE_FILTER = cel<DiskImage>((filter) =>
  filter.field('spec.lifecycle').notEquals(DiskImageLifecycle.OBSOLETE),
);

const LIFECYCLE_PREDICATE_PATTERN = /this\.spec\.lifecycle\s*(==|!=)/;

const withDefaultLifecycleFilter = (
  filter?: CelFilter<DiskImage>,
): CelFilter<DiskImage> | undefined => {
  if (filter && LIFECYCLE_PREDICATE_PATTERN.test(filter)) {
    return filter;
  }
  return filter
    ? cel<DiskImage>((builder) =>
        builder.and(builder.group(filter), DISK_IMAGE_NON_OBSOLETE_FILTER),
      )
    : DISK_IMAGE_NON_OBSOLETE_FILTER;
};

export type DiskImageListFilterCriteria = {
  search?: string;
  guestOsFamily?: GuestOSFamily;
  architecture?: Architecture[];
  lifecycle?: DiskImageLifecycle[];
  showObsolete?: boolean;
  scope?: 'global' | 'tenant';
};

const ALL_DISK_IMAGE_LIFECYCLE_VALUES = [
  DiskImageLifecycle.UNSPECIFIED,
  DiskImageLifecycle.AVAILABLE,
  DiskImageLifecycle.DEPRECATED,
  DiskImageLifecycle.OBSOLETE,
];

const lifecycleEqualsClause = (values: DiskImageLifecycle[]): CelFilter<DiskImage> | undefined =>
  cel<DiskImage>((filter) =>
    values.length === 1
      ? filter.field('spec.lifecycle').equals(values[0])
      : filter.or(...values.map((value) => filter.field('spec.lifecycle').equals(value))),
  );

export const buildDiskImageListFilter = (
  criteria: DiskImageListFilterCriteria,
): CelFilter<DiskImage> | undefined => {
  const hasCriteria = Boolean(
    criteria.search ||
    criteria.guestOsFamily !== undefined ||
    criteria.architecture?.length ||
    criteria.scope ||
    criteria.showObsolete ||
    criteria.lifecycle?.length,
  );

  if (!hasCriteria) {
    return undefined;
  }

  return cel<DiskImage>((filter) => {
    const clauses: CelFilter<DiskImage>[] = [];

    if (criteria.search) {
      clauses.push(filter.field('metadata.name').contains(criteria.search));
    }
    if (criteria.guestOsFamily !== undefined) {
      clauses.push(filter.field('spec.guestOsFamily').equals(criteria.guestOsFamily));
    }
    if (criteria.architecture?.length) {
      clauses.push(filter.field('spec.architecture').someEqualsAny(criteria.architecture, 'a'));
    }
    if (criteria.scope) {
      clauses.push(
        criteria.scope === 'global'
          ? filter.field('metadata.tenant').equals('shared')
          : filter.field('metadata.tenant').notEquals('shared'),
      );
    }

    const selectedLifecycle = criteria.lifecycle ?? [];
    if (criteria.showObsolete) {
      const lifecycleValues = selectedLifecycle.length
        ? [...selectedLifecycle, DiskImageLifecycle.OBSOLETE]
        : ALL_DISK_IMAGE_LIFECYCLE_VALUES;

      const clause = lifecycleEqualsClause(lifecycleValues);
      if (clause) {
        clauses.push(clause);
      }
    } else if (selectedLifecycle.length) {
      const clause = lifecycleEqualsClause(selectedLifecycle);
      if (clause) {
        clauses.push(clause);
      }
    }

    return filter.and(...clauses);
  });
};

export const useDiskImages = (
  params: DiskImageListParams = {},
  options: DiskImagesQueryOptions = {},
) => {
  const client = useApiFetch(DiskImages);
  const effectiveParams = { ...params, filter: withDefaultLifecycleFilter(params.filter) };
  return useApiQuery({
    queryKey: apiQueryKey('v1/disk_images', undefined, effectiveParams),
    queryFn: () => client.list(effectiveParams),
    placeholderData: keepPreviousData,
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

export const useDiskImage = (id?: string) => {
  const client = useApiFetch(DiskImages);
  return useApiQuery({
    queryKey: apiQueryKey('v1/disk_images', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: !!id,
  });
};

export const invalidateDiskImagesQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/disk_images') });

export const useCreateDiskImage = () => {
  const client = useApiFetch(DiskImages);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (body: MessageInitShape<typeof DiskImageSchema>) => {
      const resp = await client.create({ object: body });
      if (!resp.object) {
        throw new Error('Create response missing disk image object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateDiskImagesQueries(qc),
  });
};

export type UpdateDiskImageInput = {
  id: string;
  body: MessageInitShape<typeof DiskImageSchema>;
};

export const useUpdateDiskImage = () => {
  const client = useApiFetch(DiskImages);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: UpdateDiskImageInput) => {
      const resp = await client.update({
        object: { id, ...body },
        updateMask: { paths: buildUpdateMaskPaths(body as Record<string, unknown>) },
      });
      if (!resp.object) {
        throw new Error('Update response missing object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateDiskImagesQueries(qc),
  });
};
