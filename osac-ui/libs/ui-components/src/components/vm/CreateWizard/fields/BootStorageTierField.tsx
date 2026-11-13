import { useTranslation } from '../../../../hooks/useTranslation';
import {
  StorageTierSelectField,
  type StorageTierSelectFieldProps,
} from '../../../Form/StorageTierSelectField';

type BootStorageTierFieldProps = Omit<StorageTierSelectFieldProps, 'fieldId' | 'label'>;

const BootStorageTierField = (props: BootStorageTierFieldProps) => {
  const { t } = useTranslation();

  return (
    <StorageTierSelectField
      {...props}
      label={t('Boot storage tier')}
      fieldId="vm-boot-disk-storage-tier"
    />
  );
};

export default BootStorageTierField;
