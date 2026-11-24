import { MemoryRouter } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ResourceNameField, { type ResourceNameFieldProps } from './ResourceNameField';

const namedResource = { id: 'res-1', metadata: { name: 'web-01' } };

const renderResourceNameField = (props: ResourceNameFieldProps) =>
  render(
    <MemoryRouter>
      <ResourceNameField {...props} />
    </MemoryRouter>,
  );

describe('ResourceNameField', () => {
  describe('rendering', () => {
    it('renders the resource metadata name as text when no title or details URL is provided', () => {
      renderResourceNameField({ resource: namedResource });

      expect(screen.getByText('web-01')).toBeInTheDocument();
      expect(screen.queryByRole('link')).not.toBeInTheDocument();
    });

    it('falls back to the resource id when metadata name is missing', () => {
      renderResourceNameField({ resource: { id: 'res-1' } });

      expect(screen.getByText('res-1')).toBeInTheDocument();
    });

    it('does not render a secondary line when subTitle is omitted', () => {
      renderResourceNameField({ resource: namedResource, title: 'Web server' });

      expect(screen.getByText('Web server')).toBeInTheDocument();
      expect(screen.queryByText('web-01')).not.toBeInTheDocument();
      expect(screen.queryByText('Web server', { selector: 'small' })).not.toBeInTheDocument();
    });

    it('renders the title as the primary label and the subTitle as secondary text', () => {
      renderResourceNameField({
        resource: namedResource,
        title: 'Web server',
        subTitle: 'web-01',
      });

      expect(screen.getByText('Web server')).toBeInTheDocument();
      expect(screen.getByText('web-01')).toBeInTheDocument();
      expect(screen.queryByRole('link')).not.toBeInTheDocument();
    });

    it('renders a link with the resource name when a details URL is provided without a title', () => {
      renderResourceNameField({ resource: namedResource, detailsUrl: '/vms/vm-1' });

      expect(screen.getByRole('link', { name: 'web-01' })).toHaveAttribute('href', '/vms/vm-1');
      expect(screen.queryByText('web-01', { selector: 'small' })).not.toBeInTheDocument();
    });

    it('renders a link with the title and the subTitle as secondary text', () => {
      renderResourceNameField({
        resource: namedResource,
        title: 'Web server',
        subTitle: 'web-01',
        detailsUrl: '/vms/vm-1',
      });

      expect(screen.getByRole('link', { name: 'Web server' })).toHaveAttribute('href', '/vms/vm-1');
      expect(screen.getByText('web-01')).toBeInTheDocument();
    });

    it('wraps long names instead of truncating by character count', () => {
      const longName = 'instance-type-e2e-resize-cpu-up-memory-down';

      renderResourceNameField({ resource: { id: 'res-1', metadata: { name: longName } } });

      expect(screen.getByText(longName)).toHaveClass('resource-name-field__clamped');
      expect(document.querySelector('.pf-v6-c-truncate')).toBeNull();
    });
  });
});
