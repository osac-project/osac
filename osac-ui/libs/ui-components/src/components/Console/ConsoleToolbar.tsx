import { useState } from 'react';
import {
  Button,
  MenuToggle,
  Select,
  SelectList,
  SelectOption,
  Toolbar,
  ToolbarContent,
  ToolbarItem,
} from '@patternfly/react-core';
import PasteIcon from '@patternfly/react-icons/dist/esm/icons/paste-icon';

import type { ConsoleTransport, ConsoleUiConnectionState } from './console.types';
import { useTranslation } from '../../hooks/useTranslation';

interface Props {
  connectionState: ConsoleUiConnectionState;
  onPaste?: () => void;
  consoleTransport: ConsoleTransport;
  onConsoleTransportChange: (transport: ConsoleTransport) => void;
}

const ConsoleToolbar = ({
  connectionState,
  onPaste,
  consoleTransport,
  onConsoleTransportChange,
}: Props) => {
  const { t } = useTranslation();
  const isConnected = connectionState === 'connected';

  const [isTransportOpen, setIsTransportOpen] = useState(false);
  // SelectOption values are ConsoleTransport strings, so onSelect passes the choice through.
  const transportLabels: Record<ConsoleTransport, string> = {
    serial: t('Serial console'),
    vnc: t('VNC console'),
  };

  return (
    <Toolbar>
      <ToolbarContent>
        <ToolbarItem>
          <Button
            variant="secondary"
            icon={<PasteIcon />}
            isDisabled={!isConnected || !onPaste}
            onClick={onPaste}
          >
            {t('Paste from clipboard')}
          </Button>
        </ToolbarItem>
        <ToolbarItem>
          <Select
            isOpen={isTransportOpen}
            selected={consoleTransport}
            onOpenChange={setIsTransportOpen}
            onSelect={(_event, value) => {
              setIsTransportOpen(false);
              const transport = String(value);
              if (transport === 'vnc' || transport === 'serial') {
                onConsoleTransportChange(transport);
              }
            }}
            toggle={(toggleRef) => (
              <MenuToggle
                ref={toggleRef}
                variant="secondary"
                aria-label={t('Select console type')}
                isExpanded={isTransportOpen}
                onClick={() => setIsTransportOpen((open) => !open)}
              >
                {transportLabels[consoleTransport]}
              </MenuToggle>
            )}
            shouldFocusToggleOnSelect
          >
            <SelectList>
              <SelectOption value="serial" isSelected={consoleTransport === 'serial'}>
                {transportLabels.serial}
              </SelectOption>
              <SelectOption value="vnc" isSelected={consoleTransport === 'vnc'}>
                {transportLabels.vnc}
              </SelectOption>
            </SelectList>
          </Select>
        </ToolbarItem>
      </ToolbarContent>
    </Toolbar>
  );
};

export default ConsoleToolbar;
