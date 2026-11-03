declare module '@/uni_modules/nerae-secure-vault' {
  export type SecureVaultResult = { ok: boolean; errorCode: string; found: boolean; value: string }
  export type VersionedSecureVaultResult = SecureVaultResult & { revision: string }
  export function readAuthenticationState(): SecureVaultResult
  export function writeAuthenticationState(value: string): SecureVaultResult
  export function clearAuthenticationState(): SecureVaultResult
  export function readDeviceIdentityRegistry(): SecureVaultResult
  export function compareAndSetDeviceIdentityRegistry(expectedFound: boolean, expectedValue: string, value: string): SecureVaultResult
  export function readTrainingDraft(key: string): SecureVaultResult
  export function discoverTrainingDrafts(subjectId: string, cursor: string): SecureVaultResult
  export function createTrainingDraft(key: string, value: string): SecureVaultResult
  export function writeTrainingDraft(key: string, value: string): SecureVaultResult
  export function readTrainingOperationJournal(): SecureVaultResult
  export function compareAndSetTrainingOperationJournal(expectedFound: boolean, expectedValue: string, value: string): SecureVaultResult
  export function readVersionedTrainingOperationJournal(): VersionedSecureVaultResult
  export function compareAndSetVersionedTrainingOperationJournal(expectedRevision: string, expectedFound: boolean, expectedValue: string, newFound: boolean, newValue: string): VersionedSecureVaultResult
}
