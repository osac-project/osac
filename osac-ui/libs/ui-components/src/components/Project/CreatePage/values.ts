export interface ProjectCreateValues {
  metadata: {
    name: string;
    project: string;
  };
  title: string;
}

export const initialValues: ProjectCreateValues = {
  metadata: { name: '', project: '' },
  title: '',
};
