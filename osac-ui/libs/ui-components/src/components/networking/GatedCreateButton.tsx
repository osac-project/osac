import { Tooltip } from '@patternfly/react-core';

import CreateButton, { type CreateButtonProps } from '../Primitives/CreateButton';

interface GatedCreateButtonProps extends CreateButtonProps {
  /** When set, the button is disabled and this text says what to do first. */
  disabledReason?: string;
}

/**
 * A create button that is disabled with an explanation until its parent is ready. The
 * button stays focusable (isAriaDisabled), so the reason is reachable from the keyboard.
 */
const GatedCreateButton = ({
  disabledReason,
  isDisabled,
  onClick,
  ...props
}: GatedCreateButtonProps) => {
  const button = (
    <CreateButton
      {...props}
      onClick={disabledReason ? undefined : onClick}
      isDisabled={isDisabled || Boolean(disabledReason)}
    />
  );

  return disabledReason ? <Tooltip content={disabledReason}>{button}</Tooltip> : button;
};

export default GatedCreateButton;
