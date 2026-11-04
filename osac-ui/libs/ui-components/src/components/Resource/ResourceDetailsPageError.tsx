import { useNavigate } from 'react-router-dom';
import type { EmptyStateProps } from '@patternfly/react-core';
import ExclamationTriangleIcon from '@patternfly/react-icons/dist/esm/icons/exclamation-triangle-icon';
import SearchIcon from '@patternfly/react-icons/dist/esm/icons/search-icon';

import QueryErrorPage from './QueryErrorPage';
import { isUnauthorizedError } from '../../utils/unauthorizedError';

type ResourceDetailsPageErrorVariant = 'load-error' | 'not-found' | 'unauthorized';

type ResourceDetailsPageErrorProps = {
  parentTo: string;
  parentLabel: string;
  resourceLabel?: string;
  onRetry?: () => void;
  sectionClassName?: string;
} & ({ error: unknown; variant?: never } | { variant: 'not-found'; error?: never });

const resolveVariant = (error: unknown): Exclude<ResourceDetailsPageErrorVariant, 'not-found'> =>
  isUnauthorizedError(error) ? 'unauthorized' : 'load-error';

const variantConfig = (
  resourceLabel: string,
): Record<
  Exclude<ResourceDetailsPageErrorVariant, 'unauthorized'>,
  {
    status: 'danger' | 'warning';
    title: string;
    body: string;
    icon: NonNullable<EmptyStateProps['icon']>;
  }
> => ({
  'load-error': {
    status: 'danger',
    title: `Could not load ${resourceLabel}`,
    body: `Unable to load this ${resourceLabel} right now.`,
    icon: ExclamationTriangleIcon,
  },
  'not-found': {
    status: 'warning',
    title: `${resourceLabel.charAt(0).toUpperCase()}${resourceLabel.slice(1)} not found`,
    body: `This ${resourceLabel} could not be found.`,
    icon: SearchIcon,
  },
});

export const ResourceDetailsPageError = ({
  parentTo,
  parentLabel,
  resourceLabel = 'resource',
  onRetry,
  sectionClassName,
  ...props
}: ResourceDetailsPageErrorProps) => {
  const navigate = useNavigate();
  const variant = props.variant ?? resolveVariant(props.error);
  const returnAction = {
    label: `Return to ${parentLabel.toLowerCase()}`,
    onClick: () => navigate(parentTo),
  };

  if (variant === 'unauthorized') {
    return <QueryErrorPage error={props.error} sectionClassName={sectionClassName} />;
  }

  const { status, title, body, icon } = variantConfig(resourceLabel)[variant];

  return (
    <QueryErrorPage
      error={props.error}
      title={title}
      body={body}
      status={status}
      icon={icon}
      onRetry={variant === 'load-error' ? onRetry : undefined}
      secondaryAction={returnAction}
      sectionClassName={sectionClassName}
    />
  );
};
