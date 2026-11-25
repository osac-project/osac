interface NetworkIdentity {
  id: string;
  metadata?: { name?: string };
  spec?: { ipv4Cidr?: string };
}

/** `name (IPv4 CIDR)`, so a picker distinguishes same-named networks by their range. */
export const virtualNetworkOptionLabel = (network: NetworkIdentity): string => {
  const name = network.metadata?.name?.trim() || network.id;
  const cidr = network.spec?.ipv4Cidr?.trim();
  return cidr ? `${name} (${cidr})` : name;
};
