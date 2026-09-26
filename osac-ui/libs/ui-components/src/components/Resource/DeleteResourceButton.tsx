import { FC } from 'react';
import {
  Button,
  ButtonProps,
  DropdownItem,
  DropdownItemProps,
  Flex,
  FlexItem,
} from '@patternfly/react-core';
import DumpsterIcon from '@patternfly/react-icons/dist/esm/icons/dumpster-icon';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

type DeleteResourceButtonProps = {
  canDelete?: boolean;
  showIcon?: boolean;
} & (({ isDropdown?: false } & ButtonProps) | ({ isDropdown: true } & DropdownItemProps));

const DeleteResourceButton: FC<DeleteResourceButtonProps> = (allProps) => {
  const { t } = useTranslation();
  const { isDropdown, canDelete = true, showIcon, children, ...rest } = allProps;

  if (isDropdown) {
    return (
      <DropdownItem
        isDanger
        isDisabled={!canDelete}
        value="delete"
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
  }

  return (
    <Button
      isDanger
      isDisabled={!canDelete}
      variant="danger"
      icon={showIcon ? <DumpsterIcon /> : undefined}
      {...(rest as ButtonProps)}
    >
      {children ?? t('Delete')}
    </Button>
  );
};

export default DeleteResourceButton;
