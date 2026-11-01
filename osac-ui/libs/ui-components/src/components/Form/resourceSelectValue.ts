export interface ResourceSelectValue {
  id: string;
  name: string;
}

export const emptyResourceSelectValue = (): ResourceSelectValue => ({ id: '', name: '' });

export const resourceSelectValueFromReference = (
  reference?: Partial<ResourceSelectValue>,
): ResourceSelectValue => ({
  id: reference?.id ?? '',
  name: reference?.name ?? '',
});
