declare module '@/uni_modules/nerae-secure-vault' {
  export type SecureVaultResult = { ok: boolean; errorCode: string; found: boolean; value: string }
  export function readAuthenticationState(): SecureVaultResult
  export function writeAuthenticationState(value: string): SecureVaultResult
  export function clearAuthenticationState(): SecureVaultResult
}
