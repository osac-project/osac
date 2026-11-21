// Playwright debug output can include DOM values and protocol arguments.
const debug = process.env.DEBUG ?? '';
const pwDebug =
  process.env.PWDEBUG ?? process.env.npm_config_pwdebug ?? process.env.npm_package_config_pwdebug;
if (debug.includes('*') || /(^|[\s,])pw(?::|$)/i.test(debug) || (pwDebug && pwDebug !== '0')) {
  throw new Error(
    'Playwright debug logging and inspector must be disabled for credentialed tests.',
  );
}

// Playwright 1.64's failure context includes an accessibility snapshot, which
// can contain password input values even with screenshots and traces disabled.
// This internal switch is covered by the password-report regression test.
process.env.PLAYWRIGHT_NO_COPY_PROMPT = '1';
