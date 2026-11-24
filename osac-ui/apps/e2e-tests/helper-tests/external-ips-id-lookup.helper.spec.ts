import { type Page, expect, test } from '@playwright/test';

import { readExternalIpId } from '../../../libs/test-utils/src/external_ips.page';

interface PageRequest {
  limit: number;
  start: number;
}

const stubPage = (respond: (request: PageRequest) => unknown, calls: PageRequest[]): Page =>
  ({
    evaluate: (_callback: unknown, argument: PageRequest) => {
      calls.push({ limit: argument.limit, start: argument.start });
      return Promise.resolve(respond(argument));
    },
  }) as unknown as Page;

const otherItem = (index: number) => ({ id: `id-${index}`, metadata: { name: `other-${index}` } });

test('finds a match on the second page and requests the expected offset', async () => {
  const calls: PageRequest[] = [];
  const page = stubPage(({ limit, start }) => {
    if (start === 0) {
      return { items: Array.from({ length: limit }, (_, index) => otherItem(index)), total: 101 };
    }
    return { items: [{ id: 'target-id', metadata: { name: 'external-ip-e2e-1' } }], total: 101 };
  }, calls);

  await expect(readExternalIpId(page, 'external-ip-e2e-1')).resolves.toBe('target-id');
  expect(calls).toEqual([
    { limit: 100, start: 0 },
    { limit: 100, start: 100 },
  ]);
});

test('returns undefined once the reported total is consumed', async () => {
  const calls: PageRequest[] = [];
  const page = stubPage(
    ({ limit }) => ({
      items: Array.from({ length: limit }, (_, index) => otherItem(index)),
      total: 2,
    }),
    calls,
  );

  await expect(readExternalIpId(page, 'missing')).resolves.toBeUndefined();
  expect(calls).toEqual([{ limit: 100, start: 0 }]);
});
