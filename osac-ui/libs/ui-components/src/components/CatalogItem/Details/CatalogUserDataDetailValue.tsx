import type { UserDataSource } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import UserDataDetailValue from '../../UserData/UserDataDetailValue';
import type { CatalogFieldPolicyLike } from '../catalogItemPolicy';
import { catalogUserDataFieldFromPolicy } from '../catalogUserData';

interface CatalogUserDataDetailValueProps {
  policy: CatalogFieldPolicyLike<UserDataSource>;
}

const CatalogUserDataDetailValue = ({ policy }: CatalogUserDataDetailValueProps) => {
  const { t } = useTranslation();
  const userData = catalogUserDataFieldFromPolicy(policy).value;

  return <UserDataDetailValue userData={userData} emptyValue={t('Not set')} />;
};

export default CatalogUserDataDetailValue;
