import Foundation
import JavaScriptCore

/// Additional duplicate-key and depth checks for small native request bodies.
/// Foundation validates JSON syntax, but its object decoder can accept and
/// collapse duplicate keys. This scanner does not rewrite the request bytes.
enum NeraeStrictJSON {
    /// The journal's TypeScript decoder accepts exactly JSON.stringify bytes.
    /// Use the system JavaScript engine on a bounded, already-scanned document
    /// so native recovery cannot claim a body that JS cannot later decode.
    static func isCanonicalJournalObject(_ value: String) -> Bool {
        guard isObjectWithoutDuplicateKeys(value, asciiKeysOnly: true),
              let context = JSContext() else { return false }
        context.exceptionHandler = { _, _ in }
        guard let stringify = context.evaluateScript("(function (s) { try { return JSON.stringify(JSON.parse(s)); } catch (_) { return null; } })"),
              let result = stringify.call(withArguments: [value]), result.isString,
              let normalized = result.toString() else { return false }
        return normalized.utf8.elementsEqual(value.utf8)
    }

    static func isObjectWithoutDuplicateKeys(_ value: String, asciiKeysOnly: Bool = false) -> Bool {
        let data = Data(value.utf8)
        guard !data.isEmpty, data.count <= 64 * 1024 else { return false }
        // Enforce recursion and node limits before asking Foundation to decode
        // the whole document; Foundation remains the final syntax authority.
        var scanner = Scanner(bytes: Array(data), asciiKeysOnly: asciiKeysOnly)
        guard scanner.scan(), let object = try? JSONSerialization.jsonObject(with: data) else { return false }
        return object is [String: Any]
    }

    private struct Scanner {
        let bytes: [UInt8]
        let asciiKeysOnly: Bool
        var index = 0
        var nodes = 0

        mutating func scan() -> Bool {
            whitespace()
            guard index < bytes.count, bytes[index] == 0x7b, value(depth: 0) else { return false }
            whitespace()
            return index == bytes.count
        }

        private mutating func value(depth: Int) -> Bool {
            guard depth <= 32 else { return false }
            nodes += 1
            guard nodes <= 12_000 else { return false }
            whitespace()
            guard index < bytes.count else { return false }
            switch bytes[index] {
            case 0x7b: return object(depth: depth)
            case 0x5b: return array(depth: depth)
            case 0x22: return stringRange() != nil
            default:
                let start = index
                while index < bytes.count && ![0x20, 0x09, 0x0a, 0x0d, 0x2c, 0x5d, 0x7d].contains(bytes[index]) { index += 1 }
                return index > start
            }
        }

        private mutating func object(depth: Int) -> Bool {
            index += 1
            whitespace()
            if take(0x7d) { return true }
            var names = Set<Data>()
            while true {
                whitespace()
                guard let range = stringRange() else { return false }
                var wrapped = Data([0x5b])
                wrapped.append(contentsOf: bytes[range])
                wrapped.append(0x5d)
                guard let parsed = try? JSONSerialization.jsonObject(with: wrapped) as? [String], parsed.count == 1 else { return false }
                let name = Data(parsed[0].utf8)
                guard (!asciiKeysOnly || name.allSatisfy({ $0 < 0x80 })), names.insert(name).inserted else { return false }
                whitespace()
                guard take(0x3a), value(depth: depth + 1) else { return false }
                whitespace()
                if take(0x7d) { return true }
                guard take(0x2c) else { return false }
            }
        }

        private mutating func array(depth: Int) -> Bool {
            index += 1
            whitespace()
            if take(0x5d) { return true }
            var count = 0
            while true {
                count += 1
                guard count <= 5000, value(depth: depth + 1) else { return false }
                whitespace()
                if take(0x5d) { return true }
                guard take(0x2c) else { return false }
            }
        }

        private mutating func stringRange() -> Range<Int>? {
            guard take(0x22) else { return nil }
            let start = index - 1
            while index < bytes.count {
                let byte = bytes[index]
                if byte == 0x22 {
                    index += 1
                    return start..<index
                }
                guard byte >= 0x20 else { return nil }
                if byte == 0x5c {
                    index += 1
                    guard index < bytes.count else { return nil }
                }
                index += 1
            }
            return nil
        }

        private mutating func whitespace() {
            while index < bytes.count && [0x20, 0x09, 0x0a, 0x0d].contains(bytes[index]) { index += 1 }
        }

        private mutating func take(_ byte: UInt8) -> Bool {
            guard index < bytes.count, bytes[index] == byte else { return false }
            index += 1
            return true
        }
    }
}
