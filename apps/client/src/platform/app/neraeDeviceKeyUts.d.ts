declare module '@/uni_modules/nerae-device-key' {
  export type DeviceKeyResult = {
    ok: boolean
    errorCode: string
    alias: string
    publicKey: { kty: string; crv: string; x: string; y: string }
    proof: string
  }
  export function createDeviceKeyAtReservedAlias(alias: string): DeviceKeyResult
  export function readExistingDevicePublicKey(alias: string): DeviceKeyResult
  export function signExistingDeviceProof(alias: string, purpose: 'email_login' | 'phone_login' | 'approval_exchange' | 'recovery_code', resourceId: string, nonce: string): DeviceKeyResult
}
