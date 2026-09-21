import { MessageInitShape } from '@bufbuild/protobuf';

import { BareMetalInstanceTypeSchema } from '@osac/types/private';

import { BareMetalInstanceTypeFormValues } from './values';
import { KeyValuePair } from '../../Form/KeyValueMapField';

const pairsToRecord = (pairs: KeyValuePair[]): { [key: string]: string } =>
  Object.fromEntries(
    pairs.filter((pair) => pair.key.trim() !== '').map((pair) => [pair.key, pair.value]),
  );

export const toRequestBody = (
  values: BareMetalInstanceTypeFormValues,
): MessageInitShape<typeof BareMetalInstanceTypeSchema> => ({
  metadata: { name: values.metadata.name },
  spec: {
    description: values.spec.description,
    hardware: {
      cpu: {
        cores: values.spec.cpu.cores,
        architecture: values.spec.cpu.architecture,
        model: values.spec.cpu.model,
        threadsPerCore: values.spec.cpu.threadsPerCore,
      },
      memory: {
        totalGb: values.spec.memory.totalGb,
        type: values.spec.memory.type,
      },
      disks: values.spec.disks.map((disk) => ({
        type: disk.type,
        capacityGb: disk.capacityGb,
        interface: disk.interface,
      })),
      accelerators: values.spec.accelerators.map((accelerator) => ({
        type: accelerator.type,
        model: accelerator.model,
        ...(accelerator.vendor !== undefined ? { vendor: accelerator.vendor } : {}),
        ...(accelerator.memoryGb !== undefined ? { memoryGb: accelerator.memoryGb } : {}),
      })),
      networkPorts: values.spec.networkPorts.map((port) => ({
        name: port.name,
        role: port.role,
        type: port.type,
        speed: port.speed,
      })),
      capabilities: pairsToRecord(values.spec.capabilities),
    },
    hostLabelSelector: { matchLabels: pairsToRecord(values.spec.hostLabelSelector) },
  },
});
