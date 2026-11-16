export interface InstanceTypeCreateFormValues {
  metadata: { name: string };
  spec: {
    description: string;
    vcpus: number | undefined;
    memoryGib: number | undefined;
    gpu: { pciDeviceSelector: string; resourceName: string; count: number | undefined };
  };
}

export const instanceTypeCreateValues: InstanceTypeCreateFormValues = {
  metadata: { name: '' },
  spec: {
    description: '',
    vcpus: undefined,
    memoryGib: undefined,
    gpu: { pciDeviceSelector: '', resourceName: '', count: undefined },
  },
};
