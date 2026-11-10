import { describe, expect, it } from 'vitest';

import { parseHttpErrorBody } from './error';

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
