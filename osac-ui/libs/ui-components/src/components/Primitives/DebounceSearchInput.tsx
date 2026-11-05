import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { SearchInput, type SearchInputProps } from '@patternfly/react-core';

const DEFAULT_DEBOUNCE_MS = 300;

export interface DebounceSearchInputProps extends Omit<
  SearchInputProps,
  'value' | 'onChange' | 'onClear'
> {
  value: string;
  onChange: (value: string) => void;
  debounceMs?: number;
  /** Bump when the value is reset outside of user text input. */
  resetKey?: string | number;
}

const DebounceSearchInput = ({
  value,
  onChange,
  debounceMs = DEFAULT_DEBOUNCE_MS,
  resetKey,
  ...searchInputProps
}: DebounceSearchInputProps) => {
  const [inputValue, setInputValue] = useState(value);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const valueRef = useRef(value);
  valueRef.current = value;

  useLayoutEffect(() => {
    clearTimeout(debounceRef.current);
    debounceRef.current = undefined;
    valueRef.current = value;
    setInputValue(value);
  }, [value, resetKey]);

  const scheduleChange = useCallback(
    (next: string) => {
      clearTimeout(debounceRef.current);
      const valueAtSchedule = valueRef.current;
      debounceRef.current = setTimeout(() => {
        if (valueRef.current !== valueAtSchedule) {
          return;
        }
        onChange(next);
      }, debounceMs);
    },
    [debounceMs, onChange],
  );

  useEffect(() => () => clearTimeout(debounceRef.current), []);

  const handleClear = () => {
    clearTimeout(debounceRef.current);
    debounceRef.current = undefined;
    setInputValue('');
    onChange('');
  };

  return (
    <SearchInput
      {...searchInputProps}
      value={inputValue}
      onChange={(_event, next) => {
        setInputValue(next);
        scheduleChange(next);
      }}
      onClear={handleClear}
    />
  );
};

export default DebounceSearchInput;
