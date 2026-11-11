import type { TFunction } from 'i18next';
import * as Yup from 'yup';

export const INT32_MIN = -(2 ** 31);
export const INT32_MAX = 2 ** 31 - 1;
export const INT64_MIN = -(1n << 63n);
export const INT64_MAX = (1n << 63n) - 1n;

// protobuf int64
export const bigIntSchema = (t: TFunction, min = 1n, max = INT64_MAX) => {
  const minimum = min < INT64_MIN ? INT64_MIN : min;
  const maximum = max > INT64_MAX ? INT64_MAX : max;

  return Yup.mixed<bigint>()
    .test(
      'bigint',
      t('Must be a whole number'),
      (value) => value === undefined || typeof value === 'bigint',
    )
    .test(
      'minimum',
      minimum === 1n
        ? t('Must be greater than zero')
        : t('Must be at least {{min}}', { min: `${minimum}` }),
      (value) => value === undefined || (typeof value === 'bigint' && value >= minimum),
    )
    .test(
      'maximum',
      t('Must be at most {{max}}', { max: `${maximum}` }),
      (value) => value === undefined || (typeof value === 'bigint' && value <= maximum),
    );
};

// protobuf int32
export const intSchema = (t: TFunction, min = 1, max = INT32_MAX) => {
  const minimum = Math.max(min, INT32_MIN);
  const maximum = Math.min(max, INT32_MAX);

  return Yup.number()
    .typeError(t('Must be a whole number'))
    .test(
      'integer',
      t('Must be a whole number'),
      (value) => value === undefined || Number.isInteger(value),
    )
    .min(
      minimum,
      minimum === 1
        ? t('Must be greater than zero')
        : t('Must be at least {{min}}', { min: minimum }),
    )
    .max(maximum, t('Must be at most {{max}}', { max: maximum }));
};
