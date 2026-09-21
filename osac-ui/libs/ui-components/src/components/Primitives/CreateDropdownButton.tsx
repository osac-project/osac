import { PropsWithChildren, useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import PlusIcon from '@patternfly/react-icons/dist/esm/icons/plus-icon';

export interface CreateDropdownButtonItem {
  to: string;
  title: string;
}

export interface CreateDropdownButtonProps {
  items: CreateDropdownButtonItem[];
}

const CreateDropdownButton = ({
  items,
  children,
}: PropsWithChildren<CreateDropdownButtonProps>) => {
  const [isOpen, setIsOpen] = useState(false);

  return (
    <Dropdown
      isOpen={isOpen}
      onOpenChange={setIsOpen}
      onSelect={() => {
        setIsOpen(false);
      }}
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          variant="primary"
          icon={<PlusIcon />}
          isExpanded={isOpen}
          onClick={() => setIsOpen((open) => !open)}
        >
          {children}
        </MenuToggle>
      )}
    >
      <DropdownList>
        {items.map(({ to, title }) => (
          <DropdownItem key={to} value={to} to={to}>
            {title}
          </DropdownItem>
        ))}
      </DropdownList>
    </Dropdown>
  );
};

export default CreateDropdownButton;
