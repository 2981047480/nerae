declare module '@/uni_modules/nerae-random' {
  export function generateSecureRandomRequestKey(): { ok: boolean; errorCode: string; value: string }
}
