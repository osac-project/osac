import { Button, WizardStep } from '@patternfly/react-core';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ResourceWizardShell from './ResourceWizardShell';
import { renderWithProviders } from '../../test-utils/TestProviders';

interface Values {
  name: string;
}

describe('ResourceWizardShell', () => {
  it('renders a generic resource wizard with its supplied title and steps', () => {
    renderWithProviders(
      <ResourceWizardShell<Values, 'general' | 'review'>
        initialValues={{ name: '' }}
        validationSchema={undefined}
        onSubmit={() => Promise.resolve()}
        parentTo="/resources"
        parentLabel="Resources"
        title="Resource wizard"
        navAriaLabel="Resource wizard steps"
        onStepChange={() => undefined}
        footer={<Button>Save resource</Button>}
      >
        <WizardStep id="general" name="General">
          <div>General fields</div>
        </WizardStep>
        <WizardStep id="review" name="Review">
          <div>Review fields</div>
        </WizardStep>
      </ResourceWizardShell>,
      { routerEntries: ['/resources/create'] },
    );

    expect(screen.getByRole('heading', { name: 'Resource wizard' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resources' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Resource wizard steps' })).toBeInTheDocument();
    expect(screen.getByText('General fields')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save resource' })).toBeInTheDocument();
  });
});
