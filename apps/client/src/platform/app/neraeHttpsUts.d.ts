declare module '@/uni_modules/nerae-https/utssdk' {
  export type HttpsRequest = {
    baseUrl: string
    path: string
    method: 'GET' | 'POST'
    headersJson: string
    body: string
    timeoutMs: number
  }
  export type HttpsResponse = { requestId: string; status: number; headersJson: string; body: string }
  export type HttpsErrorCode = 'unsupported_platform' | 'invalid_configuration' | 'invalid_request' |
    'network' | 'timeout' | 'cancelled' | 'response_too_large' | 'invalid_response' | 'unavailable'
  export type HttpsFailure = { requestId: string; code: HttpsErrorCode }
  export type HttpsCallbacks = {
    success: (response: HttpsResponse) => void
    fail: (error: HttpsFailure) => void
  }
  export function request(request: HttpsRequest, callbacks: HttpsCallbacks): string
  export function cancel(requestId: string): void
}
