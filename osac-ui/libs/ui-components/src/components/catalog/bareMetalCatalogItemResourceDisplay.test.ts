import { describe, expect, it } from 'vitest';

import type {
  BareMetalInstance,
  BareMetalInstanceCatalogItem,
  BareMetalInstanceType,
  BareMetalInstanceTypeReference,
} from '@osac/types';

import {
  bareMetalInstanceTypeReferenceFromPolicy,
  findBareMetalInstanceTypeForReference,
  findBareMetalInstanceTypeReference,
} from './bareMetalCatalogItemResourceDisplay';

type InstanceTypePolicy = Parameters<typeof bareMetalInstanceTypeReferenceFromPolicy>[0];

const lockedTypeRef = {
  id: 'gpu-large',
  name: 'gpu-large',
  project: 'default',
  shared: false,
} as BareMetalInstanceTypeReference;

const instanceTypes: BareMetalInstanceType[] = [
  {
    id: 'gpu-large',
    metadata: { name: 'gpu-large' },
    spec: { description: '', hardware: undefined },
  } as BareMetalInstanceType,
];

describe('bareMetalCatalogItemResourceDisplay', () => {
  it('reads instance type references from locked and editable field policies', () => {
    expect(
      bareMetalInstanceTypeReferenceFromPolicy({
        behavior: { case: 'locked', value: lockedTypeRef },
      } as InstanceTypePolicy),
    ).toEqual(lockedTypeRef);
    expect(
      bareMetalInstanceTypeReferenceFromPolicy({
        behavior: { case: 'editable', value: { defaultValue: lockedTypeRef } },
      } as InstanceTypePolicy),
    ).toEqual(lockedTypeRef);
    expect(bareMetalInstanceTypeReferenceFromPolicy(undefined)).toBeUndefined();
  });

  it('resolves instance types by id or metadata name', () => {
    expect(findBareMetalInstanceTypeForReference(instanceTypes, lockedTypeRef)).toBe(
      instanceTypes[0],
    );
    expect(
      findBareMetalInstanceTypeForReference(instanceTypes, {
        id: '',
        name: 'gpu-large',
        project: 'default',
        shared: false,
      } as BareMetalInstanceTypeReference),
    ).toBe(instanceTypes[0]);
    expect(findBareMetalInstanceTypeForReference(instanceTypes, undefined)).toBeUndefined();
  });

  it('prefers the instance spec reference over catalog item policies', () => {
    const directRef = { id: 'cpu-small', name: 'cpu-small' } as BareMetalInstanceTypeReference;
    const instance = {
      spec: {
        instanceType: directRef,
        catalogItem: { id: 'catalog-1', name: 'catalog-item' },
      },
    } as BareMetalInstance;
    const catalogItems = [
      {
        id: 'catalog-1',
        metadata: { name: 'catalog-item' },
        fields: {
          instanceType: {
            behavior: { case: 'locked', value: lockedTypeRef },
          },
        },
      } as BareMetalInstanceCatalogItem,
    ];

    expect(findBareMetalInstanceTypeReference(instance, catalogItems)).toEqual(directRef);
  });

  it('derives the instance type reference from a matching catalog item when spec omits it', () => {
    const instance = {
      spec: {
        catalogItem: { id: 'catalog-1', name: 'bare-metal-gpu-training-server' },
      },
    } as BareMetalInstance;
    const catalogItems = [
      {
        id: 'catalog-1',
        metadata: { name: 'bare-metal-gpu-training-server' },
        fields: {
          instanceType: {
            behavior: { case: 'locked', value: lockedTypeRef },
          },
        },
      } as BareMetalInstanceCatalogItem,
    ];

    expect(findBareMetalInstanceTypeReference(instance, catalogItems)).toEqual(lockedTypeRef);
    expect(
      findBareMetalInstanceTypeForReference(
        instanceTypes,
        findBareMetalInstanceTypeReference(instance, catalogItems),
      ),
    ).toBe(instanceTypes[0]);
  });
});
