import { FC, useMemo } from 'react';
import { Link, LinkProps } from 'react-router-dom';
import { Button, ButtonProps } from '@patternfly/react-core';
import { RhUiRocketFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-rocket-fill-icon';

import {
  CatalogItem,
  getCatalogCreateActionPath,
} from '@osac/ui-components/components/catalog/catalogItemDisplay';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

interface CatalogItemLaunchButtonProps extends ButtonProps {
  catalogItem: CatalogItem;
}

const CatalogItemLaunchButton: FC<CatalogItemLaunchButtonProps> = ({
  catalogItem,
  variant = 'secondary',
  icon,
  ...rest
}) => {
  const { t } = useTranslation();
  const to = getCatalogCreateActionPath(catalogItem);
  const Component = useMemo(() => {
    const LaunchLink = (props: LinkProps) => <Link {...props} to={to} />;
    return LaunchLink;
  }, [to]);

  return (
    <Button {...rest} variant={variant} icon={icon || <RhUiRocketFillIcon />} component={Component}>
      {t('Launch instance')}
    </Button>
  );
};

export default CatalogItemLaunchButton;
