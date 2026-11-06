import { TFunction } from 'i18next';
import * as Yup from 'yup';

import { GuestOSFamily } from '@osac/types';

const USER_DATA_MAX_BYTES = 65536;

const isWellFormedXml = (value: string): boolean => {
  const document = new DOMParser().parseFromString(value, 'application/xml');
  return document.getElementsByTagName('parsererror').length === 0;
};

export const userDataSchema = (t: TFunction, guestOsFamily: GuestOSFamily | undefined) =>
  Yup.string()
    .test('user-data-max-bytes', t('User data must not exceed 64 KB.'), (value) => {
      if (!value) {
        return true;
      }
      return new TextEncoder().encode(value).byteLength <= USER_DATA_MAX_BYTES;
    })
    .test('user-data-xml', t('Enter valid XML.'), (value) => {
      if (guestOsFamily !== GuestOSFamily.GUEST_OS_FAMILY_WINDOWS) {
        return true;
      }
      if (!value) {
        return true;
      }
      return isWellFormedXml(value.trim());
    });
