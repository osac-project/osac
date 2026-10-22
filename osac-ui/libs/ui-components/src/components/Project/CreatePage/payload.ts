import { ProjectCreateValues } from './values';

/**
 * Project Create encodes hierarchy in metadata.name (dot-separated path).
 * The server derives metadata.project from all segments except the last.
 * See PrivateProjectsServer.Create — metadata.project must be empty or match that prefix.
 */
export const getCreateProjectPayload = (values: ProjectCreateValues) => {
  const name = values.metadata.project
    ? `${values.metadata.project}.${values.metadata.name}`
    : values.metadata.name;

  return {
    metadata: {
      name,
      project: '',
    },
    spec: {
      title: values.title,
      ...(values.description && { description: values.description }),
    },
  };
};
