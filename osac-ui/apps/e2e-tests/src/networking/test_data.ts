/**
 * Seed values for the External IP UI tests.
 *
 * The networking suite is independent of the VMaaS `extravars.yaml`: operators
 * provide these values through environment variables so a run never depends on
 * a resource left behind by another suite.
 */

const resolveInstance = (): string => {
  const instance = process.env.OSAC_E2E_INSTANCE?.trim() || '1';
  if (!/^[a-z0-9]{1,8}$/.test(instance)) {
    throw new Error('OSAC_E2E_INSTANCE must be 1-8 lowercase letters or digits.');
  }
  return instance;
};

const instance = resolveInstance();

const withInstance = (base: string): string => `${base}-${instance}`;

const configuredPool = process.env.OSAC_EXT_IP_POOL?.trim() ?? '';
const configuredCidr = process.env.OSAC_EXT_IP_CIDR?.trim() ?? '';
const configuredProject = process.env.OSAC_EXT_IP_PROJECT?.trim() ?? '';

/** Unique External IP name for this run. */
export const externalIpName = withInstance('external-ip-e2e');

/** Pool the suite consumes: a configured pool, or the one it provisions itself. */
export const externalIpPoolName = configuredPool || withInstance('external-ip-pool-e2e');

/** CIDR for the self-provisioned pool; empty when a pool is reused. */
export const externalIpPoolCidr = configuredCidr;

/** Project for the created External IP; empty uses the tenant default project. */
export const externalIpProject = configuredProject;

/** Status the platform assigns once an address is allocated. */
export const externalIpAllocatedStatus = 'Allocated';

/** Status the platform assigns once a pool is ready for allocation. */
export const externalIpPoolReadyStatus = 'Ready';

export const externalIpPoolIsReused = configuredPool !== '';

export const externalIpPoolIsProvisioned = !externalIpPoolIsReused && configuredCidr !== '';

/**
 * Reason the pool-provisioning specs must skip, or `undefined` when the suite
 * creates its own pool.
 */
export const externalIpPoolProvisioningSkipReason = (): string | undefined => {
  if (externalIpPoolIsReused) {
    return `OSAC_EXT_IP_POOL is set, so the suite reuses pool "${externalIpPoolName}".`;
  }
  if (!externalIpPoolIsProvisioned) {
    return (
      'Set OSAC_EXT_IP_CIDR to let the suite create its own pool, ' +
      'or OSAC_EXT_IP_POOL to reuse an existing one.'
    );
  }
  return undefined;
};

/** Tenant name whose break-glass credentials the tenant-admin specs use. */
export const externalIpTenantName = process.env.OSAC_E2E_TENANT_NAME?.trim() ?? '';

/** Optional second tenant admin, used to assert cross-tenant denial. */
export const secondTenantAdminUsername = process.env.OSAC_E2E_SECOND_TENANT_USERNAME?.trim() ?? '';

export const secondTenantAdminPassword = process.env.OSAC_E2E_SECOND_TENANT_PASSWORD ?? '';

/**
 * Reason the cross-tenant denial check must skip, or `undefined` when a second
 * tenant admin is configured.
 */
export const secondTenantSkipReason = (): string | undefined => {
  if (!secondTenantAdminUsername || !secondTenantAdminPassword) {
    return (
      'Set OSAC_E2E_SECOND_TENANT_USERNAME and OSAC_E2E_SECOND_TENANT_PASSWORD ' +
      'to assert cross-tenant denial.'
    );
  }
  return undefined;
};
