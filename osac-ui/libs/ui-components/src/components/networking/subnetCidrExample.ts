const IPV4_CIDR = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/;

/**
 * The longest prefix the example uses. The server rejects a subnet longer than the address
 * plan's limit, and /26 is the strictest limit of every plan the installer derives.
 */
const LONGEST_EXAMPLE_PREFIX = 26;

/** An IPv4 range as its first address and size, in plain arithmetic (no 32-bit shifts). */
interface Range {
  start: number;
  size: number;
}

const parseCidr = (cidr?: string): { range: Range; prefix: number } | undefined => {
  const match = cidr?.trim().match(IPV4_CIDR);
  if (!match) {
    return undefined;
  }
  const octets = match.slice(1, 5).map(Number);
  const prefix = Number(match[5]);
  if (octets.some((octet) => octet > 255) || prefix > 32) {
    return undefined;
  }
  const address = octets.reduce((value, octet) => value * 256 + octet, 0);
  const size = 2 ** (32 - prefix);
  return { range: { start: Math.floor(address / size) * size, size }, prefix };
};

const overlaps = (a: Range, b: Range): boolean =>
  a.start < b.start + b.size && b.start < a.start + a.size;

const formatAddress = (address: number): string =>
  [24, 16, 8, 0].map((shift) => Math.floor(address / 2 ** shift) % 256).join('.');

/**
 * An example subnet CIDR for the Create subnet placeholder: the first block inside the virtual
 * network that no existing subnet uses, with a prefix two bits longer than the network's, at
 * least /24 and at most /26, so the server accepts its length. A network of /26 or longer
 * suggests its own CIDR. Returns undefined when the CIDR is not valid IPv4, is longer than any
 * plan allows, or has no free block of that size.
 */
export const subnetCidrExample = (
  parentCidr?: string,
  existingCidrs: readonly string[] = [],
): string | undefined => {
  const parent = parseCidr(parentCidr);
  if (!parent || parent.prefix > 28) {
    return undefined;
  }
  const examplePrefix = Math.max(
    parent.prefix,
    Math.min(Math.max(parent.prefix + 2, 24), LONGEST_EXAMPLE_PREFIX),
  );
  const used = existingCidrs
    .map((cidr) => parseCidr(cidr)?.range)
    .filter((range): range is Range => range !== undefined);
  const size = 2 ** (32 - examplePrefix);
  const end = parent.range.start + parent.range.size;
  for (let start = parent.range.start; start < end; start += size) {
    if (!used.some((range) => overlaps(range, { start, size }))) {
      return `${formatAddress(start)}/${examplePrefix}`;
    }
  }
  return undefined;
};
