import Foundation
import Security

public enum NeraeDeviceProofPurpose: String {
    case emailLogin = "email_login"
    case phoneLogin = "phone_login"
    case approvalExchange = "approval_exchange"
    case recoveryCode = "recovery_code"
}

public struct NeraeDeviceProof: CustomStringConvertible, CustomDebugStringConvertible {
    private let compact: String
    fileprivate init(_ compact: String) { self.compact = compact }
    public var description: String { "<redacted device proof>" }
    public var debugDescription: String { description }

    // Only send this value in the TLS request body. Never write it to logs.
    public func forRequest() -> String { compact }
}

enum NeraeProofBuilder {
    enum Error: Swift.Error { case invalidInput, randomUnavailable }

    private struct Header: Encodable {
        let alg = "ES256"
        let typ = "JWT"
    }
    private struct Claims: Encodable {
        let aud = "nerae-auth-device"
        let purpose: String
        let resourceId: String
        let nonce: String
        let iat: Int64
        let exp: Int64
        let jti: String
    }

    static func make(purpose: NeraeDeviceProofPurpose, resourceID: String, nonce: String,
                     now: Date = Date(), signDER: (Data) throws -> Data) throws -> NeraeDeviceProof {
        guard validOpaque(resourceID, minimum: 22, maximum: 128),
              validOpaque(nonce, minimum: 43, maximum: 256) else { throw Error.invalidInput }
        let seconds = now.timeIntervalSince1970
        guard seconds.isFinite, seconds >= 0, seconds < 100_000_000_000 - 60 else {
            throw Error.invalidInput
        }
        let iat = Int64(seconds.rounded(.down))
        var random = [UInt8](repeating: 0, count: 32)
        let status = random.withUnsafeMutableBytes { buffer in
            SecRandomCopyBytes(kSecRandomDefault, buffer.count, buffer.baseAddress!)
        }
        guard status == errSecSuccess else { throw Error.randomUnavailable }
        let claims = Claims(purpose: purpose.rawValue, resourceId: resourceID, nonce: nonce,
                            iat: iat, exp: iat + 60, jti: base64URL(Data(random)))
        let encoder = JSONEncoder()
        let header = try encoder.encode(Header())
        let payload = try encoder.encode(claims)
        let message = base64URL(header) + "." + base64URL(payload)
        let signature = try P256SignatureEncoding.rawSignature(fromDER: signDER(Data(message.utf8)))
        return NeraeDeviceProof(message + "." + base64URL(signature))
    }
}

func base64URL(_ data: Data) -> String {
    data.base64EncodedString().replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
}

func validOpaque(_ value: String, minimum: Int, maximum: Int) -> Bool {
    let bytes = value.utf8
    return (minimum...maximum).contains(bytes.count) && bytes.allSatisfy {
        (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45 || $0 == 95
    }
}
