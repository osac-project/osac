import { screen, within } from '@testing-library/react';
import { expect } from 'vitest';

/** Typeahead MenuToggle puts the value on the combobox, not the labeled toggle button. */
const menuToggleRoot = (labeled: HTMLElement, label: RegExp | string): HTMLElement => {
  const root = labeled.closest('.pf-v6-c-menu-toggle');
  if (!(root instanceof HTMLElement)) {
    throw new Error(`No MenuToggle root for resource select label ${String(label)}`);
  }
  return root;
};

export const getResourceSelectCombobox = (label: RegExp | string) => {
  const labeled = screen.getByLabelText(label);
  return within(menuToggleRoot(labeled, label)).getByRole('combobox');
};

export const getAllResourceSelectComboboxes = (label: RegExp | string) =>
  screen
    .getAllByLabelText(label)
    .map((labeled) => within(menuToggleRoot(labeled, label)).getByRole('combobox'));

/** PatternFly typeahead disables via `pf-m-disabled` on the MenuToggle, not HTML disabled on the labeled control. */
export const expectResourceSelectDisabled = (label: RegExp | string) => {
  const labeled = screen.getByLabelText(label);
  expect(labeled.closest('.pf-v6-c-menu-toggle')).toHaveClass('pf-m-disabled');
};

export const expectResourceSelectEnabled = (label: RegExp | string) => {
  const labeled = screen.getByLabelText(label);
  expect(labeled.closest('.pf-v6-c-menu-toggle')).not.toHaveClass('pf-m-disabled');
};
