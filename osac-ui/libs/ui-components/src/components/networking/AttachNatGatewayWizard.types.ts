export interface AttachNatGatewayVirtualNetwork {
  id: string;
  metadata?: { name?: string; project?: string; tenant?: string };
  spec?: { ipv4Cidr?: string };
}

export interface AttachNatGatewayFormValues {
  metadata: {
    name: string;
  };
  externalIpId: string;
}

export interface AttachNatGatewayExternalIpOption {
  value: string;
  label: string;
}
