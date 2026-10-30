import Foundation
import Security

public struct NeraeDevicePublicKey: Encodable {
    public let kty = "EC"
    public let crv = "P-256"
    public let x: String
    public let y: String
}

public struct NeraeNewDeviceKey {
    // Persist this opaque identifier in the account-specific local mapping.
    // Losing it must lead to device recovery, not automatic key replacement.
    public let alias: String
    public let publicKey: NeraeDevicePublicKey
}

public enum NeraeDeviceKeyError: Swift.Error {
    case invalidAlias, aliasConflict, unavailable, missing, ambiguous, unexpectedKey, operationFailed
}

public enum NeraeDeviceKeyStore {
    private static let lock = NSLock()
    private static let tagPrefix = "com.nerae.device-key.v1/"

    // Call only for a deliberately new device identity. Do not call to repair
    // a missing existing key; new-device approval/recovery is required instead.
    public static func createNew() throws -> NeraeNewDeviceKey {
        var random = [UInt8](repeating: 0, count: 32)
        let status = random.withUnsafeMutableBytes { buffer in
            SecRandomCopyBytes(kSecRandomDefault, buffer.count, buffer.baseAddress!)
        }
        guard status == errSecSuccess else { throw NeraeDeviceKeyError.unavailable }
        return try create(alias: base64URL(Data(random)), conflictError: .ambiguous)
    }

    // The caller must persist this alias before invoking this create-once API.
    // On an uncertain result, recovery is readExisting only; do not call this again.
    public static func createAtReservedAlias(alias: String) throws -> NeraeNewDeviceKey {
        try create(alias: alias, conflictError: .aliasConflict)
    }

    private static func create(alias: String, conflictError: NeraeDeviceKeyError) throws -> NeraeNewDeviceKey {
        lock.lock()
        defer { lock.unlock() }
        let tag = try keyTag(alias)
        guard try find(tag: tag) == nil else { throw conflictError }
        var accessError: Unmanaged<CFError>?
        guard let access = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
                                                          [.privateKeyUsage], &accessError) else {
            throw NeraeDeviceKeyError.unavailable
        }
        let attributes: [String: Any] = [
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits as String: 256,
            kSecAttrTokenID as String: kSecAttrTokenIDSecureEnclave,
            kSecPrivateKeyAttrs as String: [
                kSecAttrIsPermanent as String: true,
                kSecAttrApplicationTag as String: tag,
                kSecAttrAccessControl as String: access
            ]
        ]
        var createError: Unmanaged<CFError>?
        guard let key = SecKeyCreateRandomKey(attributes as CFDictionary, &createError) else {
            throw NeraeDeviceKeyError.unavailable
        }
        try validate(key)
        return NeraeNewDeviceKey(alias: alias, publicKey: try publicKey(key))
    }

    public static func publicKeyForExisting(alias: String) throws -> NeraeDevicePublicKey {
        try withExisting(alias: alias) { try publicKey($0) }
    }

    public static func signForExisting(alias: String, purpose: NeraeDeviceProofPurpose,
                                       resourceID: String, nonce: String) throws -> NeraeDeviceProof {
        try withExisting(alias: alias) { key in
            try NeraeProofBuilder.make(purpose: purpose, resourceID: resourceID, nonce: nonce) { message in
                var error: Unmanaged<CFError>?
                guard let signature = SecKeyCreateSignature(key, .ecdsaSignatureMessageX962SHA256,
                                                            message as CFData, &error) else {
                    throw NeraeDeviceKeyError.operationFailed
                }
                return signature as Data
            }
        }
    }

    private static func withExisting<T>(alias: String, operation: (SecKey) throws -> T) throws -> T {
        lock.lock()
        defer { lock.unlock() }
        let tag = try keyTag(alias)
        guard let key = try find(tag: tag) else { throw NeraeDeviceKeyError.missing }
        try validate(key)
        return try operation(key)
    }

    private static func keyTag(_ alias: String) throws -> Data {
        guard Self.isCanonicalReservedAlias(alias) else { throw NeraeDeviceKeyError.invalidAlias }
        return Data((tagPrefix + alias).utf8)
    }

    static func isCanonicalReservedAlias(_ alias: String) -> Bool {
        guard validOpaque(alias, minimum: 43, maximum: 43) else { return false }
        let base64 = alias.replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/") + "="
        guard let decoded = Data(base64Encoded: base64), decoded.count == 32 else { return false }
        return base64URL(decoded) == alias
    }

    private static func find(tag: Data) throws -> SecKey? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassKey,
            kSecAttrKeyClass as String: kSecAttrKeyClassPrivate,
            kSecAttrApplicationTag as String: tag,
            kSecReturnRef as String: true,
            kSecMatchLimit as String: kSecMatchLimitAll
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let keys = result as? [SecKey] else {
            throw NeraeDeviceKeyError.unavailable
        }
        guard keys.count == 1 else { throw NeraeDeviceKeyError.ambiguous }
        return keys[0]
    }

    private static func validate(_ key: SecKey) throws {
        guard let attributes = SecKeyCopyAttributes(key) as? [String: Any],
              attributes[kSecAttrTokenID as String] as? String == kSecAttrTokenIDSecureEnclave as String,
              attributes[kSecAttrKeyType as String] as? String == kSecAttrKeyTypeECSECPrimeRandom as String,
              attributes[kSecAttrKeyClass as String] as? String == kSecAttrKeyClassPrivate as String,
              attributes[kSecAttrKeySizeInBits as String] as? Int == 256,
              SecKeyIsAlgorithmSupported(key, .sign, .ecdsaSignatureMessageX962SHA256) else {
            throw NeraeDeviceKeyError.unexpectedKey
        }
    }

    private static func publicKey(_ key: SecKey) throws -> NeraeDevicePublicKey {
        guard let publicKey = SecKeyCopyPublicKey(key) else { throw NeraeDeviceKeyError.operationFailed }
        var error: Unmanaged<CFError>?
        guard let external = SecKeyCopyExternalRepresentation(publicKey, &error) as Data?,
              external.count == 65, external[0] == 0x04 else {
            throw NeraeDeviceKeyError.operationFailed
        }
        return NeraeDevicePublicKey(x: base64URL(external.subdata(in: 1..<33)),
                                    y: base64URL(external.subdata(in: 33..<65)))
    }
}
