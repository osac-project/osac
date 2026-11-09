export const EXTERNAL_IP_POOLS_LIST_PATH = '/admin/infrastructure/external-ip-pools';

export const externalIpPoolDetailsPath = (id: string) => `${EXTERNAL_IP_POOLS_LIST_PATH}/${id}`;

export interface ExternalIpPoolFormValues {
  metadata: { name: string };
  cidrs: string[];
}

export const getExternalIpPoolValues = (): ExternalIpPoolFormValues => ({
  metadata: { name: '' },
  cidrs: [''],
});
