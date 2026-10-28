type MainPageLocator = readonly [
  role: 'link' | 'button' | 'navigation',
  options: { name: string; exact: true },
];

export const MainPage = {
  navigation: ['navigation', { name: 'Primary navigation', exact: true }],
  catalog: ['link', { name: 'Catalog', exact: true }],
  projects: ['link', { name: 'Projects', exact: true }],
  administration: ['button', { name: 'Administration', exact: true }],
  tenants: ['link', { name: 'Tenants', exact: true }],
  infrastructure: ['button', { name: 'Infrastructure', exact: true }],
  storage: ['button', { name: 'Storage', exact: true }],
  storageBackends: ['link', { name: 'Storage Backends', exact: true }],
  storageTiers: ['link', { name: 'Storage Tiers', exact: true }],
  instanceTypes: ['link', { name: 'Instance types', exact: true }],
  externalIpPools: ['link', { name: 'External IP pools', exact: true }],
  diskImages: ['link', { name: 'Disk images', exact: true }],
  networking: ['button', { name: 'Networking', exact: true }],
  virtualNetworks: ['link', { name: 'Virtual networks', exact: true }],
  securityGroups: ['link', { name: 'Security groups', exact: true }],
  externalIps: ['link', { name: 'External IPs', exact: true }],
  secrets: ['link', { name: 'Secrets', exact: true }],
} as const satisfies Record<string, MainPageLocator>;
