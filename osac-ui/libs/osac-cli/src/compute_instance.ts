import { expect } from '@playwright/test';

import { expectOsacCommandSucceeded, runOsacCommand } from './cli';

export interface InstanceTypeGpuConfiguration {
  count: number;
  resourceName: string;
  pciDeviceSelector: string;
}

export interface InstanceTypeConfiguration {
  name: string;
  vcpus: number;
  memoryGib: number;
  gpu?: InstanceTypeGpuConfiguration;
}

export interface ComputeInstanceConfiguration {
  desired: InstanceTypeConfiguration;
  active: InstanceTypeConfiguration;
}

export type InstanceTypeConfigurationExpectation = Partial<InstanceTypeConfiguration> & {
  gpu?: Partial<InstanceTypeGpuConfiguration>;
};

export interface ComputeInstanceConfigurationExpectation {
  desired?: InstanceTypeConfigurationExpectation;
  active?: InstanceTypeConfigurationExpectation;
}

interface InstanceTypeReference {
  id?: string;
  name?: string;
}

interface ComputeInstance {
  spec?: {
    instance_type?: InstanceTypeReference;
  };
  status?: {
    last_restarted_at?: string;
    instance_type?: InstanceTypeReference;
  };
}

interface InstanceTypeResource {
  metadata?: {
    name?: string;
  };
  spec?: {
    vcpus?: number;
    memory_gib?: number;
    gpu?: {
      count?: number;
      resource_name?: string;
      pci_device_selector?: string;
    };
  };
}

const parseResource = <T>(stdout: string, description: string): T => {
  const parsed: unknown = JSON.parse(stdout);
  if (Array.isArray(parsed) && parsed[0]) {
    return parsed[0] as T;
  }
  if (parsed !== null && typeof parsed === 'object') {
    const items = (parsed as { items?: unknown }).items;
    if (Array.isArray(items) && items[0]) {
      return items[0] as T;
    }
    return parsed as T;
  }
  throw new Error(`The CLI returned an unexpected response while getting ${description}.`);
};

const getComputeInstance = async (tenantName: string, name: string): Promise<ComputeInstance> => {
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
  return parseResource<ComputeInstance>(result.stdout, `compute instance '${name}'`);
};

/** Return the time the controller last processed a restart, if any. */
export const getComputeInstanceLastRestartedAt = async (
  tenantName: string,
  name: string,
): Promise<string | undefined> => {
  const computeInstance = await getComputeInstance(tenantName, name);
  return computeInstance.status?.last_restarted_at;
};

const getInstanceType = async (
  tenantName: string,
  reference: InstanceTypeReference,
  role: 'desired' | 'active',
): Promise<InstanceTypeConfiguration> => {
  const referenceName = reference.name?.trim() || reference.id?.trim();
  if (!referenceName) {
    throw new Error(`The compute instance has no ${role} instance type reference.`);
  }

  const result = await runOsacCommand([
    '--tenant',
    tenantName,
    'get',
    'instancetype',
    referenceName,
    '--output',
    'json',
  ]);
  expectOsacCommandSucceeded(result, `Get ${role} instance type '${referenceName}'`);

  const instanceType = parseResource<InstanceTypeResource>(
    result.stdout,
    `${role} instance type '${referenceName}'`,
  );
  const name = instanceType.metadata?.name ?? reference.name ?? referenceName;
  const vcpus = instanceType.spec?.vcpus;
  const memoryGib = instanceType.spec?.memory_gib;
  if (!name || typeof vcpus !== 'number' || typeof memoryGib !== 'number') {
    throw new Error(`Instance type '${referenceName}' does not contain complete sizing data.`);
  }

  const gpu = instanceType.spec?.gpu;
  if (gpu !== undefined) {
    if (
      typeof gpu.count !== 'number' ||
      typeof gpu.resource_name !== 'string' ||
      typeof gpu.pci_device_selector !== 'string'
    ) {
      throw new Error(`Instance type '${referenceName}' does not contain complete GPU data.`);
    }
  }

  return {
    name,
    vcpus,
    memoryGib,
    ...(gpu
      ? {
          gpu: {
            count: gpu.count as number,
            resourceName: gpu.resource_name as string,
            pciDeviceSelector: gpu.pci_device_selector as string,
          },
        }
      : {}),
  };
};

/**
 * Read the requested and currently active VM sizing from the OSAC API.
 *
 * `spec.instance_type` is the requested type, while `status.instance_type` is
 * the type currently running. The latter intentionally remains on the old
 * type while a running VM waits for a restart after resize.
 */
export const getComputeInstanceConfiguration = async (
  tenantName: string,
  name: string,
): Promise<ComputeInstanceConfiguration> => {
  const computeInstance = await getComputeInstance(tenantName, name);
  const desiredReference = computeInstance.spec?.instance_type;
  const activeReference = computeInstance.status?.instance_type;
  if (!desiredReference || !activeReference) {
    throw new Error(`Compute instance '${name}' does not expose both desired and active types.`);
  }

  const desiredReferenceKey = desiredReference.id ?? desiredReference.name;
  const activeReferenceKey = activeReference.id ?? activeReference.name;
  if (desiredReferenceKey && desiredReferenceKey === activeReferenceKey) {
    const configuration = await getInstanceType(tenantName, desiredReference, 'desired');
    return { desired: configuration, active: configuration };
  }

  const [desired, active] = await Promise.all([
    getInstanceType(tenantName, desiredReference, 'desired'),
    getInstanceType(tenantName, activeReference, 'active'),
  ]);
  return { desired, active };
};

/** Wait until the requested and active VM sizing match the expected values. */
export const expectComputeInstanceConfiguration = async (
  tenantName: string,
  name: string,
  expected: ComputeInstanceConfigurationExpectation,
  timeout = 180_000,
): Promise<void> => {
  await expect
    .poll(() => getComputeInstanceConfiguration(tenantName, name), {
      message: `Wait for compute instance '${name}' configuration to match the expected sizing`,
      timeout,
      intervals: [1_000, 2_000, 5_000],
    })
    .toMatchObject(expected as Record<string, unknown>);
};
