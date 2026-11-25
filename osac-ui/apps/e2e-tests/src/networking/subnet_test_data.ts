/**
 * Seed values for the Subnet UI tests.
 *
 * The suite works against a READY virtual network the deployment already has:
 * `OSAC_SUBNET_VIRTUAL_NETWORK` names it, otherwise the first READY network in
 * the create picker is used. Operators can pin the CIDR with `OSAC_SUBNET_CIDR`;
 * by default the suite submits the create page's own example, so no range is
 * hardcoded for a particular deployment.
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

/** Unique subnet name for this run. */
export const subnetName = withInstance('subnet-e2e');

/** Subnet name used by the failed-create check; never expected to exist. */
export const rejectedSubnetName = withInstance('subnet-e2e-rejected');

/** Network the gate check inspects; the tenant default network unless overridden. */
export const defaultNetworkName = process.env.OSAC_SUBNET_VIRTUAL_NETWORK?.trim() || 'default';

/** Network the create flow prefers; empty means "the first READY option". */
export const configuredNetworkName = process.env.OSAC_SUBNET_VIRTUAL_NETWORK?.trim() ?? '';

/** CIDR to submit; empty means "use the create page's example". */
export const subnetCidrOverride = process.env.OSAC_SUBNET_CIDR?.trim() ?? '';

/** Project to expect on the created row; empty means the tenant default. */
export const subnetProject = process.env.OSAC_SUBNET_PROJECT?.trim() ?? '';

/** Status the platform assigns once a subnet is provisioned. */
export const subnetReadyStatus = 'Ready';

/** Message the injected create failure returns. */
export const injectedCreateFailureMessage = 'subnet CIDR is outside the parent range';
