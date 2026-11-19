import { Code, ConnectError } from '@connectrpc/connect';
import { describe, expect, it } from 'vitest';

import { getErrorMessage, parseHttpErrorBody } from './error';

describe('parseHttpErrorBody', () => {
  it('extracts message from gRPC-gateway JSON errors', () => {
    expect(
      parseHttpErrorBody(
        `{"code":3, "message":"field 'user_data' is required but no value was provided and no default is defined"}`,
      ),
    ).toBe("field 'user_data' is required but no value was provided and no default is defined");
  });

  it('returns plain text bodies unchanged', () => {
    expect(parseHttpErrorBody('upstream unavailable')).toBe('upstream unavailable');
  });
});

describe('getErrorMessage', () => {
  it('leaves ordinary ConnectError raw messages unchanged', () => {
    expect(
      getErrorMessage(new ConnectError('cannot change GPU configuration', Code.FailedPrecondition)),
    ).toBe('cannot change GPU configuration');
  });
});
