import { cancel, request } from '@/uni_modules/nerae-https'
import { createAppNativeHttpsPorts } from './nativeHttpsTransport'

/**
 * App-only binding to the native HTTPS UTS module. The server base is an
 * explicit caller-supplied value; this module never invents a production URL.
 */
export function createConfiguredAppHttpsPorts(baseUrl: string, timeoutMs = 30_000) {
  return createAppNativeHttpsPorts(baseUrl, request, cancel, timeoutMs)
}
