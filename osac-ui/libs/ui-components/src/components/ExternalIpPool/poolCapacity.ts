import type { ExternalIPPool } from '@osac/types/private';

export interface PoolCapacity {
  available: bigint;
  total: bigint;
  used: bigint;
  /** Whole percent of the pool that is available; "<1" when some but under 1%. */
  availablePercent: string;
  /** Percent of the pool that is used, for a progress bar. */
  usedPercent: number;
}

const percentText = (part: bigint, total: bigint): string => {
  const percent = Math.round((Number(part) / Number(total)) * 100);
  if (percent === 0 && part > 0n) {
    return '<1';
  }
  if (percent === 100 && part < total) {
    return '>99';
  }
  return `${percent}`;
};

/** The pool's capacity, or undefined while it has no status or no addresses. */
export const poolCapacity = (pool: ExternalIPPool): PoolCapacity | undefined => {
  const status = pool.status;
  if (!status || status.total <= 0n) {
    return undefined;
  }
  const used = status.total - status.available;
  return {
    available: status.available,
    total: status.total,
    used,
    availablePercent: percentText(status.available, status.total),
    usedPercent: (Number(used) / Number(status.total)) * 100,
  };
};
