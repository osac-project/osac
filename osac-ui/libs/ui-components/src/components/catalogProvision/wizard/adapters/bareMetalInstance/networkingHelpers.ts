import type { NetworkInterface } from '@osac/types';

export const LIFECYCLE_INTERFACE_ROLE = 'lifecycle';

/**
 * Filters out lifecycle interfaces from a BareMetalInstanceType's interface list.
 * Only interfaces with roles other than 'lifecycle' are tenant-attachable.
 */
export const getAttachableInterfaces = (
  interfaces: NetworkInterface[] | undefined,
): NetworkInterface[] => {
  if (!interfaces) {
    return [];
  }
  return interfaces.filter((iface) => iface.role !== LIFECYCLE_INTERFACE_ROLE);
};

/**
 * Formats a network interface for display in select dropdowns.
 * Format: "name (role — description)"
 * Example: "data-0 (fabric — 100GbE fabric interface)"
 */
export const formatNetworkInterfaceLabel = (iface: NetworkInterface): string => {
  const role = iface.role || 'unknown';
  const description = iface.description || '';
  return `${iface.name} (${role}${description ? ` — ${description}` : ''})`;
};
