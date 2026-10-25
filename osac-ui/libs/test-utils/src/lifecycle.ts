export const lifecycleActionOrder = ['Deprecate', 'Obsolete', 'Reactivate'] as const;

export type LifecycleAction = (typeof lifecycleActionOrder)[number];
