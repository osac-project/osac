import { FC } from 'react';
import {
  Button,
  ButtonProps,
  DropdownItem,
  DropdownItemProps,
  Flex,
  FlexItem,
  Tooltip,
} from '@patternfly/react-core';
import DumpsterIcon from '@patternfly/react-icons/dist/esm/icons/dumpster-icon';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

type DeleteResourceButtonProps = {
  canDelete?: boolean;
  disabledReason?: string;
  showIcon?: boolean;
} & (({ isDropdown?: false } & ButtonProps) | ({ isDropdown: true } & DropdownItemProps));

const DeleteResourceButton: FC<DeleteResourceButtonProps> = (allProps) => {
  const { t } = useTranslation();
  const {
    isDropdown,
    canDelete = true,
    showIcon,
    children,
    disabledReason,
    isDisabled,
    ...rest
  } = allProps;

  let component: React.ReactNode;

  if (isDropdown) {
    component = (
      <DropdownItem
        isDanger={canDelete}
        isAriaDisabled={!canDelete || isDisabled}
        {...(rest as DropdownItemProps)}
      >
        {showIcon ? (
          <Flex gap={{ default: 'gapXs' }} flexWrap={{ default: 'nowrap' }}>
            <FlexItem>
              <DumpsterIcon />
            </FlexItem>
            <FlexItem>{t('Delete')}</FlexItem>
          </Flex>
        ) : (
          (children ?? t('Delete'))
        )}
      </DropdownItem>
    );
  } else {
    component = (
      <Button
        isDanger
        isAriaDisabled={!canDelete || isDisabled}
        variant="danger"
        icon={showIcon ? <DumpsterIcon /> : undefined}
        {...(rest as ButtonProps)}
      >
        {children ?? t('Delete')}
      </Button>
    );
  }

  return disabledReason ? <Tooltip content={disabledReason}>{component}</Tooltip> : component;
};

export default DeleteResourceButton;
