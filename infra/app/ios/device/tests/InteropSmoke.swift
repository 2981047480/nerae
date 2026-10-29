import CryptoKit
import Darwin
import Foundation
import Security

private enum SmokeError: Error { case assertion(String), security(String, Int) }

private func securityFailure(_ operation: String, _ error: Unmanaged<CFError>?) -> SmokeError {
    guard let error = error?.takeRetainedValue() else { return .security(operation, 0) }
    return .security(operation, CFErrorGetCode(error))
}

private func require(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    if !condition() { throw SmokeError.assertion(message) }
}

@main struct InteropSmoke {
    static func main() {
        do { try run() }
        catch {
            fputs("iOS device signing smoke failed: \(error)\n", stderr)
            exit(1)
        }
    }

    private static func run() throws {
        guard CommandLine.arguments.count == 2 else { throw SmokeError.assertion("fixture path required") }
        let attributes: [String: Any] = [kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
                                         kSecAttrKeySizeInBits as String: 256]
        var error: Unmanaged<CFError>?
        guard let key = SecKeyCreateRandomKey(attributes as CFDictionary, &error) else {
            throw securityFailure("software key generation", error)
        }
        guard let publicKey = SecKeyCopyPublicKey(key) else {
            throw SmokeError.security("public key lookup", 0)
        }
        error = nil
        guard let publicBytes = SecKeyCopyExternalRepresentation(publicKey, &error) as Data? else {
            throw securityFailure("public key export", error)
        }
        try require(publicBytes.count == 65 && publicBytes[0] == 4, "public key encoding")
        let checker = try P256.Signing.PublicKey(x963Representation: publicBytes)
        func sign(_ data: Data) throws -> Data {
            var signError: Unmanaged<CFError>?
            guard let signature = SecKeyCreateSignature(key, .ecdsaSignatureMessageX962SHA256,
                                                        data as CFData, &signError) else {
                throw securityFailure("software signing", signError)
            }
            return signature as Data
        }
        for index in 0..<128 {
            let message = Data("synthetic-message-\(index)".utf8)
            let der = try sign(message)
            let expected = try P256.Signing.ECDSASignature(derRepresentation: der)
            let actual = try P256SignatureEncoding.rawSignature(fromDER: der)
            try require(actual == expected.rawRepresentation, "DER conversion")
            try require(checker.isValidSignature(expected, for: message), "signature verification")
        }
        let invalid: [[UInt8]] = [
            [], [0x30, 0], [0x30, 6, 2, 1, 0, 2, 1, 1],
            [0x30, 6, 2, 1, 0x80, 2, 1, 1], [0x30, 7, 2, 2, 0, 1, 2, 1, 1],
            [0x30, 6, 2, 1, 1, 2, 1, 1, 0], [0x30, 0x81, 6, 2, 1, 1, 2, 1, 1],
            [0x30, 6, 2, 0, 2, 2, 1, 1], [0x30, 6, 2, 1, 1, 3, 1, 1]
        ]
        for bytes in invalid {
            var accepted = true
            do { _ = try P256SignatureEncoding.rawSignature(fromDER: Data(bytes)) }
            catch { accepted = false }
            try require(!accepted, "invalid DER accepted")
        }
        let resourceID = String(repeating: "A", count: 32)
        let nonce = String(repeating: "B", count: 43)
        let now = Date(timeIntervalSince1970: 1_793_260_800)
        let proof = try NeraeProofBuilder.make(purpose: .emailLogin, resourceID: resourceID,
                                               nonce: nonce, now: now, signDER: sign)
        let again = try NeraeProofBuilder.make(purpose: .emailLogin, resourceID: resourceID,
                                               nonce: nonce, now: now, signDER: sign)
        try require(proof.forRequest() != again.forRequest(), "proof must be unique")
        try require(String(describing: proof) == "<redacted device proof>" &&
                    String(reflecting: proof) == "<redacted device proof>", "proof redaction")
        var invalidInputAccepted = true
        do { _ = try NeraeProofBuilder.make(purpose: .emailLogin, resourceID: resourceID,
                                             nonce: "bad", now: now, signDER: sign) }
        catch { invalidInputAccepted = false }
        try require(!invalidInputAccepted, "invalid nonce accepted")
        let fixture: [String: Any] = [
            "key": ["kty": "EC", "crv": "P-256", "x": base64URL(publicBytes.subdata(in: 1..<33)),
                    "y": base64URL(publicBytes.subdata(in: 33..<65))],
            "proof": proof.forRequest(), "resourceId": resourceID, "nonce": nonce, "iat": Int64(1_793_260_800)
        ]
        let json = try JSONSerialization.data(withJSONObject: fixture, options: [.sortedKeys])
        try json.write(to: URL(fileURLWithPath: CommandLine.arguments[1]), options: .atomic)
        print("128 Security.framework software signatures, DER rejection and proof redaction passed")
    }
}
