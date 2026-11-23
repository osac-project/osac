import { initReactI18next } from 'react-i18next';
import i18n from 'i18next';
import { describe, expect, it } from 'vitest';

import { ExternalIPState, SecurityGroupState, SubnetState, VirtualNetworkState } from '@osac/types';

import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  defaultSecurityGroupListFilter,
  defaultSubnetListFilter,
  externalIpIdsFilter,
  securityGroupFilterForVirtualNetwork,
  securityGroupFilterForVirtualNetworkList,
  unallocatedExternalIpFilter,
  virtualNetworkFilterForSubnetList,
  virtualNetworkScopeFilter,
} from './networking';
import { escapeCelStringLiteral } from '../cel';

i18n.use(initReactI18next).init({
  initImmediate: false,
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  interpolation: { escapeValue: false },
});

describe('networking list filters', () => {
  it("finds only ready IPv4 tenant-default subnets in the tenant's default project", () => {
    expect(defaultSubnetListFilter()).toBe(
      `this.metadata.labels["osac.openshift.io/default"] == "true" && this.metadata.project == "" && this.status.state == ${SubnetState.READY} && has(this.spec.ipv4_cidr)`,
    );
  });

  it('finds the ready tenant-default security group in the subnet network', () => {
    expect(defaultSecurityGroupListFilter('vn-a')).toBe(
      `this.metadata.labels["osac.openshift.io/default"] == "true" && this.metadata.project == "" && this.spec.virtual_network.id == "vn-a" && this.status.state == ${SecurityGroupState.READY}`,
    );
  });

  it('filters virtual networks to ready state using enum integer', () => {
    expect(VIRTUAL_NETWORK_READY_LIST_FILTER).toBe(
      `this.status.state == ${VirtualNetworkState.READY}`,
    );
  });

  it('filters external IPs to allocated and unattached', () => {
    expect(unallocatedExternalIpFilter()).toBe(
      `this.status.state == ${ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED} && this.status.attached == false`,
    );
  });

  it('filters external IPs to the requested project', () => {
    expect(unallocatedExternalIpFilter('project-a')).toBe(
      `this.metadata.project == "project-a" && this.status.state == ${ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED} && this.status.attached == false`,
    );
  });

  it('filters external IPs by referenced IDs', () => {
    expect(externalIpIdsFilter(['ip-aaa', 'ip-bbb'])).toBe('this.id in ["ip-aaa", "ip-bbb"]');
  });

  it('escapes embedded quotes for CEL string literals', () => {
    expect(escapeCelStringLiteral('say "hello"')).toBe('say \\"hello\\"');
  });

  it('escapes backslashes for CEL string literals', () => {
    expect(escapeCelStringLiteral('path\\to\\thing')).toBe('path\\\\to\\\\thing');
  });

  it('combines virtual network scope and ready state for subnets', () => {
    expect(virtualNetworkFilterForSubnetList('vn-1')).toBe(
      `this.spec.virtual_network.id == "vn-1" && this.status.state == ${SubnetState.READY}`,
    );
  });

  it('escapes quotes in virtual network id when building subnet filter', () => {
    expect(virtualNetworkFilterForSubnetList('vn-"evil')).toBe(
      `this.spec.virtual_network.id == "vn-\\"evil" && this.status.state == ${SubnetState.READY}`,
    );
  });

  it('escapes CEL injection characters in virtual network id when building subnet filter', () => {
    expect(virtualNetworkFilterForSubnetList(`"'] || true || this.id in ['`)).toBe(
      `this.spec.virtual_network.id == "\\"'] || true || this.id in ['" && this.status.state == ${SubnetState.READY}`,
    );
  });

  it('scopes to the virtual network without a ready-state filter', () => {
    expect(virtualNetworkScopeFilter('vn-1')).toBe('this.spec.virtual_network.id == "vn-1"');
  });

  it('escapes CEL injection in virtual network id for the scope-only filter', () => {
    expect(virtualNetworkScopeFilter(`"'] || true || this.id in ['`)).toBe(
      `this.spec.virtual_network.id == "\\"'] || true || this.id in ['"`,
    );
  });

  it('combines virtual network scope and ready state for security groups', () => {
    expect(securityGroupFilterForVirtualNetworkList('vn-1')).toBe(
      `this.spec.virtual_network.id == "vn-1" && this.status.state == ${SecurityGroupState.READY}`,
    );
  });

  it('filters security groups by virtual network id', () => {
    expect(securityGroupFilterForVirtualNetwork('vn-123')).toBe(
      'this.spec.virtual_network.id == "vn-123"',
    );
  });

  it('escapes quotes in virtual network id for security group filter', () => {
    expect(securityGroupFilterForVirtualNetwork('vn-"evil')).toBe(
      'this.spec.virtual_network.id == "vn-\\"evil"',
    );
  });

  it('escapes CEL injection in virtual network id for security group filter', () => {
    expect(securityGroupFilterForVirtualNetwork(`"'] || true || this.id in ['`)).toBe(
      `this.spec.virtual_network.id == "\\"'] || true || this.id in ['"`,
    );
  });

  it('escapes trailing backslash in virtual network id for security group filter', () => {
    expect(securityGroupFilterForVirtualNetwork('vn-\\')).toBe(
      'this.spec.virtual_network.id == "vn-\\\\"',
    );
  });
});
