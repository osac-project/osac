import { FC, ReactNode } from 'react';
import { Button, Skeleton, Tooltip } from '@patternfly/react-core';
import { RhUiErrorFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-error-fill-icon';

import { getErrorMessage } from '@osac/ui-components/utils/error';

interface VirtualNetworkLoadedFieldProps {
  children: ReactNode;
  unavailableAriaLabel: string;
  isLoading?: boolean;
  error?: unknown;
}

const VirtualNetworkLoadedField: FC<VirtualNetworkLoadedFieldProps> = ({
  children,
  unavailableAriaLabel,
  isLoading,
  error,
}) => {
  if (isLoading) {
    return <Skeleton width="25px" />;
  }
  if (error !== null && error !== undefined) {
    return (
      <Tooltip content={getErrorMessage(error)}>
        <Button
          variant="link"
          isInline
          isDanger
          hasNoPadding
          icon={<RhUiErrorFillIcon />}
          aria-label={unavailableAriaLabel}
        />
      </Tooltip>
    );
  }
  return <>{children}</>;
};

export default VirtualNetworkLoadedField;
