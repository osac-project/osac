import type { ReactNode } from 'react';
import { Button, Popover } from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';

interface StatusWithMessageProps {
  children: ReactNode;
  /** The API `status.message`. When present the status becomes clickable. */
  message?: string;
}

/**
 * A status label that opens the API's message in a Status details popover.
 * Without a message it renders the label unchanged.
 */
const StatusWithMessage = ({ children, message }: StatusWithMessageProps) => {
  const { t } = useTranslation();

  if (!message) {
    return <>{children}</>;
  }

  return (
    <Popover
      headerContent={t('Status details')}
      bodyContent={message}
      aria-label={t('Status details')}
      hasAutoWidth
    >
      <Button variant="link" isInline>
        {children}
      </Button>
    </Popover>
  );
};

export default StatusWithMessage;
