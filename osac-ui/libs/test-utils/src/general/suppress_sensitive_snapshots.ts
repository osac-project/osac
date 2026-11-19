// Playwright debug output can include DOM values and protocol arguments.
const debug = process.env.DEBUG ?? '';
if (
  debug.includes('*') ||
  /(^|[\s,])pw(?::|$)/i.test(debug) ||
  (process.env.PWDEBUG && process.env.PWDEBUG !== '0')
) {
  throw new Error(
    'Playwright debug logging and inspector must be disabled for credentialed tests.',
  );
}

// Playwright 1.64's failure context includes an accessibility snapshot, which
// can contain password input values even with screenshots and traces disabled.
// This internal switch is covered by the password-report regression test.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = '1';
