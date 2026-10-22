import {
  type DescMessage,
  type DescMethodUnary,
  type DescService,
  type MessageInitShape,
  type MessageShape,
  create,
} from '@bufbuild/protobuf';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseMutationOptions, UseQueryOptions } from '@tanstack/react-query';

import { useApiFetch } from './api-context';
import { buildUpdateMaskPaths } from './v1/update-mask';

export type ResourceService<
  Operation extends string,
  Input extends DescMessage,
  Output extends DescMessage,
> = Omit<DescService, 'method'> & {
  method: Record<Operation, DescMethodUnary<Input, Output>>;
};

type ResourceQueryOptions<Data extends DescMessage, SelectedData = MessageShape<Data>> = Omit<
  UseQueryOptions<MessageShape<Data>, unknown, SelectedData>,
  'queryFn' | 'queryKey' | 'select' | 'placeholderData'
>;

type PaginatedListRequest = { offset?: number; limit?: number };

type PaginatedListResponse = {
  items: unknown[];
  size: number;
  total: number;
};

type PaginatedListItems<Output extends DescMessage> =
  MessageShape<Output> extends {
    items: infer Items;
  }
    ? Items
    : never;

type PaginatedListService<Input extends DescMessage, Output extends DescMessage> = ResourceService<
  'list',
  Input,
  Output
> &
  (MessageShape<Input> extends PaginatedListRequest
    ? MessageShape<Output> extends PaginatedListResponse
      ? unknown
      : never
    : never);

const FETCH_ALL_PAGE_SIZE = 100;

type ResourceMutationOptions<Data extends DescMessage, Variables> = Omit<
  UseMutationOptions<MessageShape<Data>, Error, Variables>,
  'mutationFn'
>;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const buildUpdateRequest = <Input extends DescMessage, Output extends DescMessage>(
  service: ResourceService<'update', Input, Output>,
  request: MessageInitShape<Input>,
) => {
  const objectField = service.method.update.input.field.object;
  if (objectField?.fieldKind !== 'message') {
    throw new Error(`${service.typeName}.update request is missing its resource object schema`);
  }

  const requestRecord: unknown = request;
  if (!isRecord(requestRecord) || !isRecord(requestRecord.object)) {
    throw new Error(`${service.typeName}.update request is missing its resource object`);
  }

  return {
    ...request,
    updateMask: {
      paths: buildUpdateMaskPaths(requestRecord.object, { schema: objectField.message }),
    },
  };
};

export const useListResource = <
  Input extends DescMessage,
  Output extends DescMessage,
  SelectedData = MessageShape<Output>,
>(
  service: ResourceService<'list', Input, Output>,
  request: MessageInitShape<Input> = create(service.method.list.input),
  options: ResourceQueryOptions<Output, SelectedData> = {},
  select?: (data: MessageShape<Output>) => SelectedData,
) => {
  const client = useApiFetch(service);
  const listRequest = create(service.method.list.input, request);

  return useQuery({
    ...options,
    queryKey: [service.typeName, 'list', request],
    queryFn: () => client.list(listRequest),
    select,
    placeholderData: keepPreviousData,
  });
};

/** Loads every item matching the filter, managing offset and limit internally. */
export const useListAllResources = <Input extends DescMessage, Output extends DescMessage>(
  service: PaginatedListService<Input, Output>,
  filter?: string,
) => {
  const client = useApiFetch<ResourceService<'list', Input, Output>>(service);
  const listRequest = create(service.method.list.input);

  return useQuery<MessageShape<Output>, unknown, PaginatedListItems<Output>>({
    queryKey: [service.typeName, 'list', { filter }, 'all'],
    queryFn: async ({ signal }) => {
      const listPage = (offset: number) =>
        client.list(
          { ...listRequest, filter, limit: FETCH_ALL_PAGE_SIZE, offset },
          { signal },
        ) as Promise<MessageShape<Output> & PaginatedListResponse>;
      let page = await listPage(0);
      const items = [...page.items];
      while (items.length < page.total && page.items.length > 0) {
        page = await listPage(items.length);
        items.push(...page.items);
      }
      return { ...page, items, size: items.length };
    },
    select: (response) =>
      (response as MessageShape<Output> & { items: PaginatedListItems<Output> }).items,
    placeholderData: keepPreviousData,
  });
};

export const useGetResource = <Input extends DescMessage, Output extends DescMessage>(
  service: ResourceService<'get', Input, Output>,
  request: MessageInitShape<Input>,
  options: ResourceQueryOptions<Output> = {},
) => {
  const client = useApiFetch(service);

  return useQuery({
    ...options,
    queryKey: [service.typeName, 'get', request],
    queryFn: () => client.get(request),
  });
};

export const useCreateResource = <Input extends DescMessage, Output extends DescMessage>(
  service: ResourceService<'create', Input, Output>,
  options: ResourceMutationOptions<Output, MessageInitShape<Input>> = {},
) => {
  const client = useApiFetch(service);
  const queryClient = useQueryClient();

  return useMutation({
    ...options,
    mutationFn: (request) => client.create(request),
    onSuccess: async (...args) => {
      await queryClient.invalidateQueries({ queryKey: [service.typeName] });
      await options.onSuccess?.(...args);
    },
  });
};

export const useUpdateResource = <Input extends DescMessage, Output extends DescMessage>(
  service: ResourceService<'update', Input, Output>,
  options: ResourceMutationOptions<Output, MessageInitShape<Input>> = {},
) => {
  const client = useApiFetch(service);
  const queryClient = useQueryClient();

  return useMutation({
    ...options,
    mutationFn: (request) => client.update(buildUpdateRequest(service, request)),
    onSuccess: async (...args) => {
      await queryClient.invalidateQueries({ queryKey: [service.typeName] });
      await options.onSuccess?.(...args);
    },
  });
};

export const useDeleteResource = <Input extends DescMessage, Output extends DescMessage>(
  service: ResourceService<'delete', Input, Output>,
  options: ResourceMutationOptions<Output, MessageInitShape<Input>> = {},
) => {
  const client = useApiFetch(service);
  const queryClient = useQueryClient();

  return useMutation({
    ...options,
    mutationFn: (request) => client.delete(request),
    onSuccess: async (...args) => {
      await queryClient.invalidateQueries({ queryKey: [service.typeName] });
      await options.onSuccess?.(...args);
    },
  });
};

export const useInvalidateServiceQueries = () => {
  const queryClient = useQueryClient();
  return (service: { typeName: string }) =>
    queryClient.invalidateQueries({ queryKey: [service.typeName] });
};
