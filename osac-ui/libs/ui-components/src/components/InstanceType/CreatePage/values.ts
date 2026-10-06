export interface InstanceTypeCreateFormValues {
  metadata: { name: string };
  spec: {
    description: string;
    vcpus: string;
    memoryGib: string;
    gpu: { pciDeviceSelector: string; resourceName: string; count: string };
  };
}

export const instanceTypeCreateValues: InstanceTypeCreateFormValues = {
  metadata: { name: '' },
  spec: {
    description: '',
    vcpus: '',
    memoryGib: '',
    gpu: { pciDeviceSelector: '', resourceName: '', count: '' },
  },
};
