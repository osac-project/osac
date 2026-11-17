import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ClusterSchema, Secrets } from '@osac/types';

import ClusterSettingsSection from './ClusterSettingsSection';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterSettingsSection', () => {
  it('shows only the typed inline SSH public key and both CIDRs without fetching secrets', () => {
    const transport = createMockConnectTransport();
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(
      <ClusterSettingsSection
        cluster={create(ClusterSchema, {
          spec: {
            sshPublicKey: 'ssh-ed25519 PUBLIC_EXAMPLE_ONLY example',
            network: { podCidr: '10.128.0.0/14', serviceCidr: '172.30.0.0/16' },
          },
        })}
      />,
      { transport },
    );
    expect(screen.getByText('ssh-ed25519 PUBLIC_EXAMPLE_ONLY example')).toBeInTheDocument();
    expect(
      screen.getByText('ssh-ed25519 PUBLIC_EXAMPLE_ONLY example').closest('dd'),
    ).toHaveAttribute('tabindex', '0');
    expect(screen.getByText('10.128.0.0/14')).toBeInTheDocument();
    expect(screen.getByText('172.30.0.0/16')).toBeInTheDocument();
    expect(
      unary.mock.calls.some(([method]) =>
        Object.values(Secrets.method).some((secretMethod) => secretMethod === method),
      ),
    ).toBe(false);
    expect(unary).not.toHaveBeenCalled();
  });

  it('shows meaningful empty fallbacks without any secret lookup', () => {
    const transport = createMockConnectTransport();
    const unary = vi.spyOn(transport, 'unary');
    renderWithProviders(<ClusterSettingsSection cluster={create(ClusterSchema)} />, { transport });
    expect(screen.getAllByText('—')).toHaveLength(3);
    expect(
      screen.getAllByText('—').every((value) => !value.closest('dd')?.hasAttribute('tabindex')),
    ).toBe(true);
    expect(unary).not.toHaveBeenCalled();
  });
});
