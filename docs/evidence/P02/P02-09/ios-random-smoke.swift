import Foundation

@main
struct IosRandomSmoke {
    static func main() {
        let vector = Data((0..<32).map(UInt8.init))
        guard neraeBase64URL(vector) == "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8" else {
            fatalError("base64url encoding vector failed")
        }

        let generated = NeraeSecureRandomBridge.generateRequestKey()
        guard generated.range(of: "^[A-Za-z0-9_-]{43}$", options: .regularExpression) != nil else {
            fatalError("native random output format failed")
        }
        print("PASS: iOS source random output format and deterministic encoder vector; value redacted.")
    }
}
