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
        print("PASS: canonical reserved aliases accept exactly unpadded base64url encodings of 32 bytes")
    }
}
