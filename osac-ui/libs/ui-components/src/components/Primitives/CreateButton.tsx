import { useMemo } from 'react';
import { Link, LinkProps } from 'react-router-dom';
import { Button, ButtonProps, Stack, StackItem, Tooltip } from '@patternfly/react-core';
import PlusIcon from '@patternfly/react-icons/dist/esm/icons/plus-icon';

import { getErrorMessage } from '@osac/ui-components/utils/error';

import { type ServiceWithMethod, usePermissions } from '../../hooks/use-permissions';
import { useTranslation } from '../../hooks/useTranslation';

export interface CreateButtonProps extends ButtonProps {
  to?: string;
  service: ServiceWithMethod<'create'>;
}

const CreateButton = ({
  icon = <PlusIcon />,
  variant = 'primary',
  to,
  children,
  isDisabled,
  service,
  ...rest
}: CreateButtonProps) => {
  const { t } = useTranslation();
  const { allowed, error, isLoading } = usePermissions(service, 'create');
  const Component = useMemo(
    () => (to ? (props: LinkProps) => <Link {...props} to={to} /> : undefined),
    [to],
  );

  const button = (
    <Button
      {...rest}
      isAriaDisabled={isDisabled || !!error || isLoading || !allowed}
      variant={variant}
      icon={icon}
      component={Component}
    >
      {children}
    </Button>
  );

  return error ? (
    <Tooltip
      content={
        <Stack>
          <StackItem>{t('Failed to check create permissions')}</StackItem>
          <StackItem>{getErrorMessage(error)}</StackItem>
        </Stack>
      }
    >
      {button}
    </Tooltip>
  ) : (
    button
  );
};

export default CreateButton;
