export type TimestampFormat = 'Full' | 'Date' | 'Time';

export interface TimestampProps {
  value?: ProtoTimestamp | string | Date | null;
  fallback?: string;
  format?: TimestampFormat;
}

type ProtoTimestamp = {
  seconds?: bigint;
  nanos?: number;
};

const DISPLAY_FORMAT = new Intl.DateTimeFormat('en-US', {
  month: 'numeric',
  day: 'numeric',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

const DATE_FORMAT = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

const TIME_FORMAT = new Intl.DateTimeFormat('en-US', {
  hour: 'numeric',
  minute: '2-digit',
  second: '2-digit',
  hour12: true,
});

const isProtoTimestamp = (value: unknown): value is ProtoTimestamp => {
  if (!value || typeof value !== 'object') {
    return false;
  }
  return 'seconds' in value && 'nanos' in value;
};

const toDate = (value: ProtoTimestamp | string | Date): Date | undefined => {
  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? undefined : value;
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) {
      return undefined;
    }
    const parsed = Date.parse(trimmed);
    return Number.isNaN(parsed) ? undefined : new Date(parsed);
  }
  if (isProtoTimestamp(value)) {
    if (value.seconds === undefined) {
      return undefined;
    }
    const ms = Number(value.seconds) * 1000 + Math.floor((value.nanos ?? 0) / 1_000_000);
    const date = new Date(ms);
    return Number.isNaN(date.getTime()) ? undefined : date;
  }
  return undefined;
};

const getTimestampValue = (date: Date, format?: TimestampFormat) => {
  switch (format) {
    case 'Date':
      return DATE_FORMAT.format(date);
    case 'Time':
      return TIME_FORMAT.format(date);
    default:
      return DISPLAY_FORMAT.format(date);
  }
};

export const Timestamp = ({ value, format, fallback = '—' }: TimestampProps) => {
  if (!value) {
    return <>{fallback}</>;
  }

  const date = toDate(value);
  if (!date) {
    return <>{fallback}</>;
  }

  return <time dateTime={date.toISOString()}>{getTimestampValue(date, format)}</time>;
};
