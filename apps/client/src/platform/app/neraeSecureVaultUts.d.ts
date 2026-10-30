declare module '@/uni_modules/nerae-secure-vault' {
  export type SecureVaultResult = { ok: boolean; errorCode: string; found: boolean; value: string }
  export function readAuthenticationState(): SecureVaultResult
  export function writeAuthenticationState(value: string): SecureVaultResult
  export function clearAuthenticationState(): SecureVaultResult
  export function readDeviceIdentityRegistry(): SecureVaultResult
  export function compareAndSetDeviceIdentityRegistry(expectedFound: boolean, expectedValue: string, value: string): SecureVaultResult
  export function readTrainingOperationJournal(): SecureVaultResult
  export function compareAndSetTrainingOperationJournal(expectedFound: boolean, expectedValue: string, value: string): SecureVaultResult
}
