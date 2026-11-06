import { expectOsacCommandSucceeded, runOsacCommand } from './cli';

interface ComputeInstance {
  status?: {
    last_restarted_at?: string;
  };
}

/** Return the time the controller last processed a restart, if any. */
export const getComputeInstanceLastRestartedAt = async (
  tenantName: string,
  name: string,
): Promise<string | undefined> => {
  const result = await runOsacCommand([
    '--tenant',
    tenantName,
    'get',
    'computeinstance',
    name,
    '--output',
    'json',
  ]);
  expectOsacCommandSucceeded(result, `Get compute instance '${name}'`);

  const parsed: unknown = JSON.parse(result.stdout);
  const instance = (Array.isArray(parsed) ? parsed[0] : parsed) as ComputeInstance | undefined;
  if (instance === undefined || instance === null || typeof instance !== 'object') {
    throw new Error(`Compute instance '${name}' was not found in tenant '${tenantName}'.`);
  }

  return instance.status?.last_restarted_at;
};
