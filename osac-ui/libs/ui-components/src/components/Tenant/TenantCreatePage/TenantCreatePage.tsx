import { useState } from 'react';

import type { Tenant } from '@osac/types/private';
import TenantWizard from '@osac/ui-components/components/Tenant/CreateWizard/TenantWizard';

import BreakGlassCredentialModal from './BreakGlassCredentialModal';

const TenantCreatePage = () => {
  const [createdTenant, setCreatedTenant] = useState<Tenant | undefined>();

  if (createdTenant) {
    return <BreakGlassCredentialModal tenant={createdTenant} />;
  }

  return <TenantWizard onCreated={setCreatedTenant} />;
};

export default TenantCreatePage;
