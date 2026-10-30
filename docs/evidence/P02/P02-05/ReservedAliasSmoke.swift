import Foundation

@main
struct ReservedAliasSmoke {
    static func main() {
        let canonicalZero = String(repeating: "A", count: 43)
        precondition(NeraeDeviceKeyStore.isCanonicalReservedAlias(canonicalZero))
        precondition(NeraeDeviceKeyStore.isCanonicalReservedAlias(String(repeating: "A", count: 42) + "Q"))
        precondition(!NeraeDeviceKeyStore.isCanonicalReservedAlias(String(repeating: "A", count: 42) + "B"))
        precondition(!NeraeDeviceKeyStore.isCanonicalReservedAlias(String(repeating: "A", count: 42) + "+"))
        precondition(!NeraeDeviceKeyStore.isCanonicalReservedAlias(String(repeating: "A", count: 42)))
        for finalByte in UInt8.min...UInt8.max {
            var bytes = Data(repeating: 0, count: 32)
            bytes[31] = finalByte
            let alias = bytes.base64EncodedString().replacingOccurrences(of: "+", with: "-")
                .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
            precondition(NeraeDeviceKeyStore.isCanonicalReservedAlias(alias))
        }
        print("PASS: canonical reserved aliases accept exactly unpadded base64url encodings of 32 bytes")
    }
}
