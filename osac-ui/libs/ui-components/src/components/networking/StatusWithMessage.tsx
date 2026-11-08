import type { ReactNode } from 'react';
import { Button, Popover } from '@patternfly/react-core';
import OutlinedQuestionCircleIcon from '@patternfly/react-icons/dist/esm/icons/outlined-question-circle-icon';

import { useTranslation } from '../../hooks/useTranslation';

interface StatusWithMessageProps {
  /** The status label of the resource. */
  children: ReactNode;
  /** The API status message; when set, the label opens it in a popover. */
  message?: string;
}

/**
 * A networking status label that shows the API status message in a popover. A label
 * with a message carries a help icon, so it looks like something that opens.
 */
const StatusWithMessage = ({ children, message }: StatusWithMessageProps) => {
  const { t } = useTranslation();

  if (!message) {
    return <>{children}</>;
  }

  return (
    <Popover headerContent={t('Status details')} bodyContent={message}>
      <Button
        variant="link"
        isInline
        icon={<OutlinedQuestionCircleIcon aria-hidden />}
        iconPosition="end"
      >
        {children}
      </Button>
    </Popover>
  );
};

export default StatusWithMessage;
