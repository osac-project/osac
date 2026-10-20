export type InstanceTypeLifecycleState = 'Unspecified' | 'Active' | 'Deprecated' | 'Obsolete';
export type InstanceTypeLifecycleAction = 'Deprecate' | 'Obsolete' | 'Reactivate';
export type InstanceTypeLifecycleMenuAction = InstanceTypeLifecycleAction | 'Delete';

export const instanceTypeLifecycleActionsByState: Record<
  InstanceTypeLifecycleState,
  readonly InstanceTypeLifecycleMenuAction[]
> = {
  Unspecified: ['Deprecate', 'Obsolete', 'Reactivate'],
  Active: ['Deprecate', 'Obsolete'],
  Deprecated: ['Obsolete', 'Reactivate'],
  Obsolete: ['Deprecate', 'Reactivate', 'Delete'],
};

export const instanceTypeLifecycleNextState: Record<
  InstanceTypeLifecycleAction,
  Exclude<InstanceTypeLifecycleState, 'Unspecified'>
> = {
  Deprecate: 'Deprecated',
  Obsolete: 'Obsolete',
  Reactivate: 'Active',
};
