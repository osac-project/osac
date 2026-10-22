const SSH_PUBLIC_KEY_REGEX =
  /^(ssh-rsa AAAAB3NzaC1yc2|ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNT|ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTItbmlzdHAzODQAAAAIbmlzdHAzOD|ecdsa-sha2-nistp521 AAAAE2VjZHNhLXNoYTItbmlzdHA1MjEAAAAIbmlzdHA1Mj|ssh-ed25519 AAAAC3NzaC1lZDI1NTE5)[0-9A-Za-z+/]+[=]{0,3}( .*)?$/;

export const trimSshPublicKey = (value: string): string =>
  value
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .join('\n');

export const isValidSshPublicKey = (value?: string): boolean => {
  if (!value?.trim()) {
    return true;
  }
  return !!trimSshPublicKey(value).match(SSH_PUBLIC_KEY_REGEX);
};
