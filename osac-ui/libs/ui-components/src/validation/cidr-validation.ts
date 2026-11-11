import type { TFunction } from 'i18next';
import { Address4, Address6 } from 'ip-address';
import * as Yup from 'yup';

export type CidrIpFamily = 'ipv4' | 'ipv6';

/**
 * Returns true when value is empty or a valid CIDR for the requested IP family.
 */
export const isValidCidr = (value: string, ipFamily: CidrIpFamily): boolean => {
  const trimmed = value.trim();
  if (!trimmed) {
    return true;
  }
  if (!trimmed.includes('/')) {
    return false;
  }

  if (ipFamily === 'ipv4') {
    try {
      return new Address4(trimmed).isCorrect();
    } catch {
      return false;
    }
  }

  return Address6.isValid(trimmed);
};

/**
 * Returns true when two CIDRs overlap. Empty or invalid values do not overlap.
 */
export const cidrsOverlap = (left: string, right: string, ipFamily: CidrIpFamily): boolean => {
  const a = left.trim();
  const b = right.trim();
  if (!a || !b || !isValidCidr(a, ipFamily) || !isValidCidr(b, ipFamily)) {
    return false;
  }
  return hasSubnetOverlap(a, [b]);
};

/**
 * Yup schema for validating CIDR notation for the requested IP family.
 * IPv4 CIDRs must also start at their network address.
 * Use .required() when the field is mandatory.
 */
export const buildCidrSchema = (t: TFunction, ipFamily: CidrIpFamily) => {
  const testName = ipFamily === 'ipv4' ? 'valid-ipv4-cidr' : 'valid-ipv6-cidr';
  const message =
    ipFamily === 'ipv4' ? t('Invalid IPv4 CIDR notation') : t('Invalid IPv6 CIDR notation');

  const schema = Yup.string().test(testName, message, (value) => {
    if (!value) {
      return true;
    }
    return isValidCidr(value, ipFamily);
  });

  if (ipFamily === 'ipv6') {
    return schema;
  }

  return schema.test('canonical-ipv4-cidr', (value, context) => {
    if (!value?.trim() || !isValidCidr(value, 'ipv4')) {
      return true;
    }

    const address = new Address4(value.trim());
    if (address.bigInt() === address.startAddress().bigInt()) {
      return true;
    }

    return context.createError({
      message: t('Use {{cidr}} instead. CIDRs must start at the network address.', {
        cidr: address.networkForm(),
      }),
    });
  });
};

/**
 * Check if a subnet CIDR is within a parent VirtualNetwork CIDR.
 * Supports both IPv4 and IPv6.
 */
export const isSubnetWithinVN = (subnetCidr: string, vnCidr: string): boolean => {
  // Try IPv4 first
  try {
    const subnet = new Address4(subnetCidr);
    const vn = new Address4(vnCidr);

    if (!subnet.isCorrect() || !vn.isCorrect()) {
      throw new Error('Not valid IPv4');
    }

    // Subnet must have a prefix length >= VN prefix length (smaller or equal range)
    if (subnet.subnetMask < vn.subnetMask) {
      return false;
    }

    // Check if subnet's start address is within VN's range
    const subnetStart = subnet.startAddress().bigInt();
    const subnetEnd = subnet.endAddress().bigInt();
    const vnStart = vn.startAddress().bigInt();
    const vnEnd = vn.endAddress().bigInt();

    return subnetStart >= vnStart && subnetEnd <= vnEnd;
  } catch {
    // Try IPv6
    try {
      const subnet = new Address6(subnetCidr);
      const vn = new Address6(vnCidr);

      if (!Address6.isValid(subnetCidr) || !Address6.isValid(vnCidr)) {
        return false;
      }

      // Subnet must have a prefix length >= VN prefix length (smaller or equal range)
      if (subnet.subnetMask < vn.subnetMask) {
        return false;
      }

      // Check if subnet's start address is within VN's range
      const subnetStart = subnet.startAddress().bigInt();
      const subnetEnd = subnet.endAddress().bigInt();
      const vnStart = vn.startAddress().bigInt();
      const vnEnd = vn.endAddress().bigInt();

      return subnetStart >= vnStart && subnetEnd <= vnEnd;
    } catch {
      return false;
    }
  }
};

/**
 * Check if a new subnet CIDR overlaps with any existing subnet CIDRs.
 * Supports both IPv4 and IPv6.
 */
export const hasSubnetOverlap = (newCidr: string, existingCidrs: string[]): boolean => {
  if (existingCidrs.length === 0) {
    return false;
  }

  // Try IPv4 first
  try {
    const newSubnet = new Address4(newCidr);
    if (!newSubnet.isCorrect()) {
      throw new Error('Not valid IPv4');
    }

    const newStart = newSubnet.startAddress().bigInt();
    const newEnd = newSubnet.endAddress().bigInt();

    for (const existingCidr of existingCidrs) {
      try {
        const existing = new Address4(existingCidr);
        if (!existing.isCorrect()) {
          continue;
        }

        const existingStart = existing.startAddress().bigInt();
        const existingEnd = existing.endAddress().bigInt();

        // Check if ranges overlap
        if (newStart <= existingEnd && newEnd >= existingStart) {
          return true;
        }
      } catch {
        continue;
      }
    }

    return false;
  } catch {
    // Try IPv6
    try {
      const newSubnet = new Address6(newCidr);
      if (!Address6.isValid(newCidr)) {
        return false;
      }

      const newStart = newSubnet.startAddress().bigInt();
      const newEnd = newSubnet.endAddress().bigInt();

      for (const existingCidr of existingCidrs) {
        try {
          const existing = new Address6(existingCidr);
          if (!Address6.isValid(existingCidr)) {
            continue;
          }

          const existingStart = existing.startAddress().bigInt();
          const existingEnd = existing.endAddress().bigInt();

          // Check if ranges overlap
          if (newStart <= existingEnd && newEnd >= existingStart) {
            return true;
          }
        } catch {
          continue;
        }
      }

      return false;
    } catch {
      return false;
    }
  }
};
