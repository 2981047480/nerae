import Foundation

@objc(NeraeDeviceKeyBridge)
public final class NeraeDeviceKeyBridge: NSObject {
    @objc public static func createNewDeviceKey() -> String {
        do {
            let created = try NeraeDeviceKeyStore.createNew()
            return encode(ok: true, alias: created.alias, publicKey: created.publicKey)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func readExistingDevicePublicKey(_ alias: String) -> String {
        do {
            let publicKey = try NeraeDeviceKeyStore.publicKeyForExisting(alias: alias)
            return encode(ok: true, alias: alias, publicKey: publicKey)
        } catch {
            return encodeFailure(error)
        }
    }

    @objc public static func signExistingDeviceProof(_ alias: String, _ purpose: String,
                                                      _ resourceID: String, _ nonce: String) -> String {
        guard let proofPurpose = NeraeDeviceProofPurpose(rawValue: purpose) else {
            return encodeFailureCode("invalid_proof_input")
        }
        do {
            let proof = try NeraeDeviceKeyStore.signForExisting(alias: alias, purpose: proofPurpose,
                                                                 resourceID: resourceID, nonce: nonce)
            return encode(ok: true, alias: alias, proof: proof.forRequest())
        } catch {
            return encodeFailure(error)
        }
    }

    private static func encodeFailure(_ error: Error) -> String {
        if let keyError = error as? NeraeDeviceKeyError {
            switch keyError {
            case .invalidAlias: return encodeFailureCode("invalid_alias")
            case .unavailable: return encodeFailureCode("hardware_unavailable")
            case .missing: return encodeFailureCode("missing_key")
            case .ambiguous: return encodeFailureCode("ambiguous_key")
            case .unexpectedKey: return encodeFailureCode("unexpected_key")
            case .operationFailed: return encodeFailureCode("operation_failed")
            }
        }
        if let proofError = error as? NeraeProofBuilder.Error {
            switch proofError {
            case .invalidInput: return encodeFailureCode("invalid_proof_input")
            case .randomUnavailable: return encodeFailureCode("hardware_unavailable")
            }
        }
        return encodeFailureCode("operation_failed")
    }

    private static func encodeFailureCode(_ code: String) -> String {
        encode(ok: false, errorCode: code)
    }

    private static func encode(ok: Bool, errorCode: String = "", alias: String = "",
                               publicKey: NeraeDevicePublicKey? = nil, proof: String = "") -> String {
        var result: [String: Any] = [
            "ok": ok,
            "errorCode": errorCode,
            "alias": alias,
            "publicKey": ["kty": "EC", "crv": "P-256", "x": "", "y": ""],
            "proof": proof
        ]
        if let publicKey {
            result["publicKey"] = ["kty": publicKey.kty, "crv": publicKey.crv,
                                   "x": publicKey.x, "y": publicKey.y]
        }
        guard let data = try? JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]),
              let json = String(data: data, encoding: .utf8) else {
            return "{\"ok\":false,\"errorCode\":\"bridge_failure\",\"alias\":\"\",\"publicKey\":{\"kty\":\"EC\",\"crv\":\"P-256\",\"x\":\"\",\"y\":\"\"},\"proof\":\"\"}"
        }
        return json
    }
}
