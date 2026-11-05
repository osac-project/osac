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

export const getErrorMessage = (error: unknown): string => {
  let errMsg = '';

  if (error instanceof Error) {
    if ('rawMessage' in error && typeof error.rawMessage === 'string') {
      errMsg = error.rawMessage;
    } else {
      errMsg = error.message;
    }
  }
  if (typeof error === 'string') {
    errMsg = error;
  }
  if (error instanceof String) {
    errMsg = error.toString();
  }
  return errMsg || 'Unexpected error';
};
