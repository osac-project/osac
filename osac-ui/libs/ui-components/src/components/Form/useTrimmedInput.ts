import { useCallback, useState } from 'react';

/**
 * Passes each user edit's trimmed value to setValue immediately, so validation
 * sees trimmed text. The input displays the raw draft until blur, then shows
 * the supplied value. Values supplied by the caller are never trimmed; if
 * they change while editing, the new value takes precedence over the draft.
 */
export const useTrimmedInput = (value: string | undefined, setValue: (value: string) => void) => {
  const [draft, setDraft] = useState<string>();

  const onChange = (nextValue: string) => {
    setDraft(nextValue);
    setValue(nextValue.trim());
  };

  const onBlur = useCallback(() => {
    setDraft(undefined);
  }, []);

  return {
    value: (draft?.trim() === value ? draft : value) ?? '',
    onChange,
    onBlur,
  };
};
