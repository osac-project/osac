export const LoginPage = {
  brand: ['#kc-header-wrapper'],
  pageTitle: ['heading', { name: 'Sign in to your account', exact: true }],
  username: ['Username or email'],
  password: ['input[type="password"]'],
  signIn: ['button', { name: 'Sign In', exact: true }],
  accountMenu: ['button', { name: 'Account menu', exact: true }],
  osacLogo: ['.pf-v6-c-masthead__brand a[aria-label="Red Hat"]'],
} as const;
