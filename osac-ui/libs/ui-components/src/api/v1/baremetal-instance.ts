import { type BareMetalInstance, BareMetalInstances } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import { type CelFilter, cel } from '../cel';

export const useBareMetalInstances = (
  filters?: CelFilter<BareMetalInstance>,
  enabled = true,
): { instances: BareMetalInstance[]; totalItems: number; isLoading: boolean; error: unknown } => {
  const projectFilter = useProjectFilterQuery<BareMetalInstance>();
  const filter = cel<BareMetalInstance>((builder) => builder.and(projectFilter, filters));
  const totalsFilter = cel<BareMetalInstance>((builder) => builder.and(projectFilter));

  const filteredBmsQuery = useListResource(BareMetalInstances, { filter }, { enabled });
  const totalBmsQuery = useListResource(
    BareMetalInstances,
    { filter: totalsFilter, limit: 0 },
    { enabled },
  );

  const isLoading = filteredBmsQuery.isLoading || totalBmsQuery.isLoading;
  const error = filteredBmsQuery.error || totalBmsQuery.error;

  return {
    error,
    isLoading,
    instances: filteredBmsQuery.data?.items ?? [],
    totalItems: totalBmsQuery.data?.total ?? 0,
  };
};
