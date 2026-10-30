import Foundation

enum P256SignatureEncoding {
    enum Error: Swift.Error { case invalidDER }

    // Security.framework emits ASN.1 DER; an ES256 JWS uses fixed-width r || s.
    static func rawSignature(fromDER data: Data) throws -> Data {
        let bytes = [UInt8](data)
        guard bytes.count >= 8, bytes[0] == 0x30,
              bytes[1] < 0x80, Int(bytes[1]) == bytes.count - 2 else {
            throw Error.invalidDER
        }
        var cursor = 2
        func integer() throws -> [UInt8] {
            guard cursor + 2 <= bytes.count, bytes[cursor] == 0x02 else { throw Error.invalidDER }
            let length = Int(bytes[cursor + 1])
            cursor += 2
            guard (1...33).contains(length), cursor + length <= bytes.count else { throw Error.invalidDER }
            var value = Array(bytes[cursor..<(cursor + length)])
            cursor += length
            guard value[0] & 0x80 == 0 else { throw Error.invalidDER }
            if value.count > 1 && value[0] == 0 {
                guard value[1] & 0x80 != 0 else { throw Error.invalidDER }
                value.removeFirst()
            }
            guard value.count <= 32, value.contains(where: { $0 != 0 }) else { throw Error.invalidDER }
            return [UInt8](repeating: 0, count: 32 - value.count) + value
        }
        let r = try integer()
        let s = try integer()
        guard cursor == bytes.count else { throw Error.invalidDER }
        return Data(r + s)
    }
}
