import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { VolumeState } from '@osac/types';

import { VolumeStatusLabel } from './VolumeStatusLabel';
import { renderWithProviders } from '../../test-utils/TestProviders';

const getLabel = (text: string) => screen.getByText(text).closest('.pf-v6-c-label');

describe('VolumeStatusLabel', () => {
  it('maps CREATING to progressing/Creating with blue label', () => {
    renderWithProviders(<VolumeStatusLabel state={VolumeState.CREATING} />);
    expect(screen.getByText('Creating')).toBeInTheDocument();
    expect(getLabel('Creating')).toHaveClass('pf-m-blue');
  });

  it('maps AVAILABLE to ready/Available with green label', () => {
    renderWithProviders(<VolumeStatusLabel state={VolumeState.AVAILABLE} />);
    expect(screen.getByText('Available')).toBeInTheDocument();
    expect(getLabel('Available')).toHaveClass('pf-m-green');
  });

  it('maps FAILED to failed/Failed with red label', () => {
    renderWithProviders(<VolumeStatusLabel state={VolumeState.FAILED} />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(getLabel('Failed')).toHaveClass('pf-m-red');
  });

  it('maps DELETING to unspecified/Deleting with grey label', () => {
    renderWithProviders(<VolumeStatusLabel state={VolumeState.DELETING} />);
    expect(screen.getByText('Deleting')).toBeInTheDocument();
    const label = getLabel('Deleting');
    expect(label).not.toHaveClass('pf-m-blue');
    expect(label).not.toHaveClass('pf-m-green');
    expect(label).not.toHaveClass('pf-m-red');
  });

  it('maps DELETED to unspecified/Deleted with grey label', () => {
    renderWithProviders(<VolumeStatusLabel state={VolumeState.DELETED} />);
    expect(screen.getByText('Deleted')).toBeInTheDocument();
    const label = getLabel('Deleted');
    expect(label).not.toHaveClass('pf-m-blue');
    expect(label).not.toHaveClass('pf-m-green');
    expect(label).not.toHaveClass('pf-m-red');
  });

  it('maps undefined to unspecified/Unspecified', () => {
    renderWithProviders(<VolumeStatusLabel />);
    expect(screen.getByText('Unspecified')).toBeInTheDocument();
  });

  it('maps UNSPECIFIED to unspecified/Unspecified with grey label', () => {
    renderWithProviders(<VolumeStatusLabel state={VolumeState.UNSPECIFIED} />);
    expect(screen.getByText('Unspecified')).toBeInTheDocument();
    const label = getLabel('Unspecified');
    expect(label).not.toHaveClass('pf-m-blue');
    expect(label).not.toHaveClass('pf-m-green');
    expect(label).not.toHaveClass('pf-m-red');
  });
});
