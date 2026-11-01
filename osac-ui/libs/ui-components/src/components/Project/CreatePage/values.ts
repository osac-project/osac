export interface ProjectCreateValues {
  metadata: {
    name: string;
    project: string;
    description: string;
  };
  title: string;
}

export const initialValues: ProjectCreateValues = {
  metadata: { name: '', project: '', description: '' },
  title: '',
};
