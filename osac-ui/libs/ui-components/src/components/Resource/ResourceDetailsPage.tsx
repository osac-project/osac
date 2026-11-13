import { ResourceDetailsPageError } from './ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from './ResourceDetailsPageLoading';

interface ResourceDetailsPageProps {
  isLoading: boolean;
  error: unknown;
  found: boolean;
  refetch: VoidFunction;
  parentTo: string;
  parentLabel: string;
  tabLabels?: string[];
  cardCount?: number;
  resourceLabel?: string;
  sectionClassName?: string;
}

const ResourceDetailsPage = ({
  isLoading,
  error,
  found,
  refetch,
  parentTo,
  parentLabel,
  tabLabels,
  cardCount,
  resourceLabel,
  sectionClassName,
  children,
}: React.PropsWithChildren<ResourceDetailsPageProps>) => {
  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo={parentTo}
        parentLabel={parentLabel}
        tabLabels={tabLabels}
        cardCount={cardCount}
        sectionClassName={sectionClassName}
      />
    );
  }

  if (error) {
    return (
      <ResourceDetailsPageError
        parentTo={parentTo}
        parentLabel={parentLabel}
        resourceLabel={resourceLabel}
        error={error}
        onRetry={() => void refetch()}
        sectionClassName={sectionClassName}
      />
    );
  }

  if (!found) {
    return (
      <ResourceDetailsPageError
        parentTo={parentTo}
        parentLabel={parentLabel}
        resourceLabel={resourceLabel}
        variant="not-found"
        sectionClassName={sectionClassName}
      />
    );
  }

  return children;
};

export default ResourceDetailsPage;
