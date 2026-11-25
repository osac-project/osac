import { Address4 } from 'ip-address';

/** The strictest subnet prefix the installer's plans derive, so the example is never rejected. */
const MIN_EXAMPLE_PREFIX = 24;
const MAX_EXAMPLE_PREFIX = 26;

interface AddressRange {
  start: bigint;
  end: bigint;
}

const parseIPv4 = (cidr: string): Address4 | undefined => {
  const value = cidr.trim();
  if (!value) {
    return undefined;
  }
  try {
    const address = new Address4(value);
    return address.isCorrect() ? address : undefined;
  } catch {
    return undefined;
  }
};

const ipv4Range = (cidr: string): AddressRange | undefined => {
  const address = parseIPv4(cidr);
  return address
    ? { start: address.startAddress().bigInt(), end: address.endAddress().bigInt() }
    : undefined;
};

/** Two bits longer than the network, clamped to the /24–/26 range the plans expect. */
export const subnetExamplePrefix = (networkPrefix: number): number => {
  if (networkPrefix >= MAX_EXAMPLE_PREFIX) {
    return networkPrefix;
  }
  return Math.min(Math.max(networkPrefix + 2, MIN_EXAMPLE_PREFIX), MAX_EXAMPLE_PREFIX);
};

/**
 * An example block inside `networkCidr` that no subnet uses yet: the first free
 * block with a prefix two bits longer than the network (clamped to /24–/26), or
 * the network's own CIDR when it is /26 or longer. Returns `undefined` when the
 * network is not IPv4 or no block of that size is free.
 */
export const subnetCidrExample = (
  networkCidr: string,
  existingSubnetCidrs: readonly string[] = [],
): string | undefined => {
  const network = parseIPv4(networkCidr);
  if (!network) {
    return undefined;
  }

  const prefix = subnetExamplePrefix(network.subnetMask);
  const blockSize = 2n ** BigInt(32 - prefix);
  const networkStart = network.startAddress().bigInt();
  const networkEnd = network.endAddress().bigInt();
  const used = existingSubnetCidrs
    .map(ipv4Range)
    .filter((range): range is AddressRange => range !== undefined)
    .sort((left, right) => (left.start < right.start ? -1 : left.start > right.start ? 1 : 0));

  let start = networkStart;
  while (start + blockSize - 1n <= networkEnd) {
    const end = start + blockSize - 1n;
    const overlap = used.find((range) => range.start <= end && range.end >= start);
    if (!overlap) {
      return `${Address4.fromBigInt(start).address}/${prefix}`;
    }
    // Jump past the overlapping subnet and align to the next block boundary.
    const next = overlap.end + 1n;
    start = next + ((blockSize - (next % blockSize)) % blockSize);
  }

  return undefined;
};
