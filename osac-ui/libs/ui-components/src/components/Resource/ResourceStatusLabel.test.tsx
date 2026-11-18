import PauseIcon from '@patternfly/react-icons/dist/esm/icons/pause-icon';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ResourceStatusLabel } from './ResourceStatusLabel';

const getLabel = (text: string) => screen.getByText(text).closest('.pf-v6-c-label');

describe('ResourceStatusLabel', () => {
  describe('rendering', () => {
    it('renders ready as a compact green label without an icon by default', () => {
      render(<ResourceStatusLabel status="ready" text="Running" />);

      const label = getLabel('Running');
      expect(label).toHaveClass('pf-m-green', 'pf-m-compact');
      expect(label?.querySelector('.pf-v6-c-label__icon')).toBeNull();
    });

    it('renders failed as red', () => {
      render(<ResourceStatusLabel status="failed" text="Failed" />);

      expect(getLabel('Failed')).toHaveClass('pf-m-red');
    });

    it('renders progressing as blue', () => {
      render(<ResourceStatusLabel status="progressing" text="Starting" />);

      expect(getLabel('Starting')).toHaveClass('pf-m-blue');
    });

    it('renders unspecified without a status color class', () => {
      render(<ResourceStatusLabel status="unspecified" text="Unknown" />);

      const label = getLabel('Unknown');
      expect(label).not.toHaveClass('pf-m-green');
      expect(label).not.toHaveClass('pf-m-red');
      expect(label).not.toHaveClass('pf-m-blue');
    });

    it('applies a color override when provided', () => {
      render(<ResourceStatusLabel status="ready" text="Paused" color="grey" />);

      expect(getLabel('Paused')).not.toHaveClass('pf-m-green');
    });

    it('does not render a custom icon unless noIcon is false', () => {
      render(<ResourceStatusLabel status="ready" text="Paused" icon={PauseIcon} />);

      expect(getLabel('Paused')?.querySelector('.pf-v6-c-label__icon')).toBeNull();
    });
  });

  describe('conditional rendering', () => {
    it('renders the status icon when noIcon is false', () => {
      render(<ResourceStatusLabel status="ready" text="Running" noIcon={false} />);

      expect(getLabel('Running')?.querySelector('.pf-v6-c-label__icon')).not.toBeNull();
    });

    it('renders a custom icon when noIcon is false', () => {
      render(<ResourceStatusLabel status="ready" text="Paused" icon={PauseIcon} noIcon={false} />);

      expect(getLabel('Paused')?.querySelector('.pf-v6-c-label__icon')).not.toBeNull();
    });

    it('renders plain status text and icon without a label', () => {
      render(<ResourceStatusLabel status="failed" text="Failed" noIcon={false} isPlain />);

      const plainStatus = screen.getByText('Failed');
      expect(plainStatus.closest('.pf-v6-c-label')).toBeNull();
      expect(plainStatus.querySelector('.pf-v6-c-icon .pf-m-danger')).toBeInTheDocument();
    });

    it('supports a warning icon for a plain status', () => {
      render(
        <ResourceStatusLabel
          status="unspecified"
          text="Timed out"
          icon={PauseIcon}
          iconStatus="warning"
          noIcon={false}
          isPlain
        />,
      );

      expect(
        screen.getByText('Timed out').querySelector('.pf-v6-c-icon .pf-m-warning'),
      ).toBeInTheDocument();
    });

    it('renders a non-compact label when isCompact is false', () => {
      render(<ResourceStatusLabel status="ready" text="Running" isCompact={false} />);

      expect(getLabel('Running')).not.toHaveClass('pf-m-compact');
    });
  });
});
