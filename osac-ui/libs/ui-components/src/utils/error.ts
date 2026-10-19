import { Code, ConnectError } from '@connectrpc/connect';
import type { TFunction } from 'i18next';

const readJsonMessage = (value: unknown): string | undefined => {
  if (!value || typeof value !== 'object') {
    return undefined;
  }
  const message = (value as { message?: unknown }).message;
  return typeof message === 'string' && message.trim() ? message.trim() : undefined;
};

/** Extract a user-facing message from fulfillment / gRPC-gateway error bodies. */
export const parseHttpErrorBody = (body: string): string | undefined => {
  const trimmed = body.trim();
  if (!trimmed) {
    return undefined;
  }

  try {
    const parsed: unknown = JSON.parse(trimmed);
    return readJsonMessage(parsed);
  } catch {
    return trimmed;
  }
};

export const formatHttpApiErrorMessage = (status: number, body: string, statusText = ''): string =>
  parseHttpErrorBody(body) ?? `API ${status}: ${body || statusText}`;

export const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) {
    if ('rawMessage' in error && typeof error.rawMessage === 'string') {
      return error.rawMessage;
    }
    return error.message || '';
  }
  if (typeof error === 'string') {
    return error;
  }
  if (error instanceof String) {
    return error.toString();
  }
  return 'Unexpected error';
};

/** Map fulfillment API errors to translated user-facing messages. */
export const getApiErrorMessage = (error: unknown, t: TFunction): string => {
  if (!(error instanceof ConnectError)) {
    return getErrorMessage(error);
  }

  switch (error.code) {
    case Code.InvalidArgument:
    case Code.AlreadyExists:
      return getErrorMessage(error);
    case Code.PermissionDenied:
      return t('You are not authorized to access this resource.');
    case Code.Internal:
      return t('Unexpected error occurred');
    default:
      return getErrorMessage(error);
  }
};
