import { TFunction } from 'i18next';
import * as Yup from 'yup';

import { GuestOSFamily } from '@osac/types';

const USER_DATA_MAX_BYTES = 65536;

const isWellFormedXml = (value: string): boolean => {
  const document = new DOMParser().parseFromString(value, 'application/xml');
  return document.getElementsByTagName('parsererror').length === 0;
};

interface GuestOsFamilyContextValue {
  guestOsFamily?: GuestOSFamily;
}

const isGuestOsFamilyContextValue = (value: unknown): value is GuestOsFamilyContextValue =>
  typeof value === 'object' && value !== null && 'guestOsFamily' in value;

const guestOsFamilyFromContext = (from: Yup.TestContext['from']): GuestOSFamily | undefined => {
  for (const { value } of from ?? []) {
    if (isGuestOsFamilyContextValue(value)) {
      return value.guestOsFamily;
    }
  }
  return undefined;
};

export const userDataSchema = (t: TFunction) =>
  Yup.string()
    .test('user-data-max-bytes', t('User data must not exceed 64 KB.'), (value) => {
      if (!value) {
        return true;
      }
      return new TextEncoder().encode(value).byteLength <= USER_DATA_MAX_BYTES;
    })
    .test('user-data-xml', t('Enter valid XML.'), function (value) {
      if (guestOsFamilyFromContext(this.from) !== GuestOSFamily.GUEST_OS_FAMILY_WINDOWS) {
        return true;
      }
      if (!value) {
        return true;
      }
      return isWellFormedXml(value?.trim() ?? '');
    });
