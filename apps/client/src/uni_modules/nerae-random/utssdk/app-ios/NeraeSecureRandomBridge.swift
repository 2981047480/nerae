import Foundation
import Security

@objc(NeraeSecureRandomBridge)
public final class NeraeSecureRandomBridge: NSObject {
    private static let byteCount = 32

    @objc public static func generateRequestKey() -> String {
        var bytes = [UInt8](repeating: 0, count: byteCount)
        let status = bytes.withUnsafeMutableBytes { buffer in
            SecRandomCopyBytes(kSecRandomDefault, buffer.count, buffer.baseAddress!)
        }
        guard status == errSecSuccess else { return "" }
        return neraeBase64URL(Data(bytes))
    }
}

func neraeBase64URL(_ data: Data) -> String {
    data.base64EncodedString()
        .replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "/", with: "_")
        .replacingOccurrences(of: "=", with: "")
}
