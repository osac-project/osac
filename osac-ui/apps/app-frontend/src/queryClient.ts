import { QueryClient } from '@tanstack/react-query';

export const createQueryClient = () =>
  new QueryClient({
    defaultOptions: {
      queries: {
        retry: 1,
        // Deduplicate fetches when components on the same page mount together.
        staleTime: 5_000,
        refetchOnMount: true,
        refetchInterval: 10_000,
      },
    },
  });
