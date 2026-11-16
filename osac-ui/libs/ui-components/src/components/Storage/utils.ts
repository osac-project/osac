import { type StorageBackend } from '@osac/types/private';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const STORAGE_BACKENDS_LIST_PATH = '/admin/infrastructure/storage/backends';

export const storageBackendDetailsPath = (id: string) =>
  `${STORAGE_BACKENDS_LIST_PATH}/${encodeURIComponent(id)}`;

export const buildStorageBackendListFilter = (
  search: string,
): CelFilter<StorageBackend> | undefined =>
  cel<StorageBackend>((b) => (search ? b.field('metadata.name').contains(search) : undefined));
