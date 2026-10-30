const REQUEST_KEY_BYTE_LENGTH = 32
const REQUEST_KEY_PATTERN = /^[A-Za-z0-9_-]{43}$/
const BASE64URL_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'
const FAILURE_MESSAGE = 'secure_random_unavailable'

// The official @dcloudio/types signature is UniNamespace.GetRandomValuesOption.
// This module-local declaration keeps the global runtime access explicit and
// avoids relying on a simulator-only wx global in native targets.
declare const wx: {
  getRandomValues(option: UniNamespace.GetRandomValuesOption): void
}

type WxRandomApi = Pick<typeof wx, 'getRandomValues'>

function encodeBase64Url(bytes: Uint8Array): string {
  let output = ''
  for (let offset = 0; offset < bytes.length; offset += 3) {
    const first = bytes[offset]
    const hasSecond = offset + 1 < bytes.length
    const hasThird = offset + 2 < bytes.length
    const second = hasSecond ? bytes[offset + 1] : 0
    const third = hasThird ? bytes[offset + 2] : 0
    output += BASE64URL_ALPHABET[first >>> 2]
    output += BASE64URL_ALPHABET[((first & 0x03) << 4) | (second >>> 4)]
    if (hasSecond) output += BASE64URL_ALPHABET[((second & 0x0f) << 2) | (third >>> 6)]
    if (hasThird) output += BASE64URL_ALPHABET[third & 0x3f]
  }
  return output
}

/** Injection exists for synthetic tests; production calls the global WeChat API. */
export function generateWeixinRequestKeyWith(api: WxRandomApi | undefined): Promise<string> {
  return new Promise((resolve, reject) => {
    let settled = false
    const fail = (): void => {
      if (settled) return
      settled = true
      reject(new Error(FAILURE_MESSAGE))
    }

    try {
      if (!api || typeof api.getRandomValues !== 'function') {
        fail()
        return
      }
      api.getRandomValues({
        length: REQUEST_KEY_BYTE_LENGTH,
        success: result => {
          if (settled) return
          try {
            const randomValues: unknown = result?.randomValues
            if (!(randomValues instanceof ArrayBuffer) || randomValues.byteLength !== REQUEST_KEY_BYTE_LENGTH) {
              fail()
              return
            }
            const key = encodeBase64Url(new Uint8Array(randomValues))
            if (!REQUEST_KEY_PATTERN.test(key)) {
              fail()
              return
            }
            settled = true
            resolve(key)
          } catch (_error) {
            fail()
          }
        },
        fail: () => fail(),
      })
    } catch (_error) {
      fail()
    }
  })
}

export function generateWeixinRequestKey(): Promise<string> {
  try {
    return generateWeixinRequestKeyWith(wx)
  } catch (_error) {
    return Promise.reject(new Error(FAILURE_MESSAGE))
  }
}
