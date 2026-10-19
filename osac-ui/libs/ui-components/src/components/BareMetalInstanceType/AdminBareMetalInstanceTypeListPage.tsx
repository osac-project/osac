import { useNavigate } from 'react-router-dom';
import { Button } from '@patternfly/react-core';

import AdminBareMetalInstanceTypeTable from './AdminBareMetalInstanceTypeTable';
import { useAdminBareMetalInstanceTypes } from '../../api/v1/private/baremetal-instance-type';
import { useTranslation } from '../../hooks/useTranslation';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';

export const BAREMETAL_INSTANCE_TYPES_LIST_ROUTE = '/admin/infrastructure/baremetal-instance-types';

const AdminBareMetalInstanceTypeListPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: bareMetalInstanceTypes = [], isLoading, error } = useAdminBareMetalInstanceTypes();

  return (
    <ListPage
      title={t('Bare metal instance types')}
      description={t('Manage provider-defined bare metal instance types for this cloud platform.')}
      error={error}
      actions={
        <Button
          variant="primary"
          onClick={() => navigate('/admin/infrastructure/baremetal-instance-types/create')}
        >
          {t('Create bare metal instance type')}
        </Button>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <AdminBareMetalInstanceTypeTable bareMetalInstanceTypes={bareMetalInstanceTypes} />
      </ListPageBody>
    </ListPage>
  );
};

export default AdminBareMetalInstanceTypeListPage;
