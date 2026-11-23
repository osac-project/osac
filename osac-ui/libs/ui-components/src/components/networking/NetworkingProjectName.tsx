import { useTranslation } from '../../hooks/useTranslation';

interface NetworkingProjectNameProps {
  /** The resource's `metadata.project`; empty means the tenant's default project. */
  project?: string;
}

/** A resource's project, with the tenant's default project shown as Root. */
const NetworkingProjectName = ({ project }: NetworkingProjectNameProps) => {
  const { t } = useTranslation();
  return <>{project || t('Root')}</>;
};

export default NetworkingProjectName;
