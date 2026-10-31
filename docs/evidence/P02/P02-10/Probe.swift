import Foundation
import Security

private struct WireResult: Decodable {
    let ok: Bool
    let requestId: String
    let status: Int
    let headersJson: String
    let body: String
    let code: String
}

final class ProbeState {
    private let lock = NSLock()
    private var count = 0
    private var payload = ""
    func receive(_ value: String) {
        lock.lock(); count += 1; payload = value; let first = count == 1; lock.unlock()
        _ = first
    }
    func wait(seconds: Double) -> (Int, String) {
        let deadline = Date().addingTimeInterval(seconds)
        while Date() < deadline {
            lock.lock(); let done = count > 0; lock.unlock()
            if done { break }
            _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
        }
        let secondDeadline = Date().addingTimeInterval(0.35)
        while Date() < secondDeadline { _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05)) }
        lock.lock(); defer { lock.unlock() }
        return (count, payload)
    }
    func callbackCount() -> Int { lock.lock(); defer { lock.unlock() }; return count }
}

let args = CommandLine.arguments
precondition(args.count == 5)
let mode = args[1]
let base = args[2]
let anchorPath = args[3]
let timeoutMs = Int(args[4]) ?? 1_000
if anchorPath != "-", let anchor = try? Data(contentsOf: URL(fileURLWithPath: anchorPath)) {
    NeraeHttpsBridge.setTestAnchor(anchor)
} else {
    NeraeHttpsBridge.setTestAnchor(nil)
}
let path: String
switch mode {
case "success", "invalidHeader", "requestTooLarge", "requestAtLimit": path = mode == "requestTooLarge" || mode == "requestAtLimit" ? "/post-size" : "/success?probe=ok"
case "revoke": path = "/logout-empty"
case "revokeBody": path = "/logout-empty"
case "revokeBodyLength": path = "/logout-body-length"
case "revokeBodyUnframed": path = "/logout-body-unframed"
case "revokeMissingNoStore": path = "/logout-no-store"
case "revokeUnauthorized": path = "/logout-unauthorized"
case "revokeNon204": path = "/logout-200"
case "revokeCancel": path = "/logout-slow"
case "escape": path = "/../echo"
case "spacePath": path = "/bad path"
case "unicodePath": path = "/训练"
case "spaceQuery": path = "/success?q=has space"
case "unicodeQuery": path = "/success?q=训练"
case "badPercent": path = "/success?q=%2"
case "encodedQuery": path = "/success?q=%E8%AE%AD"
case "inspect": path = "/inspect"
case "redirect301": path = "/redirect-301"
case "redirect302": path = "/redirect-302"
case "crossOrigin": path = "/redirect-cross"
case "downgrade": path = "/redirect-downgrade"
case "largeFixed": path = "/large-fixed"
case "largeChunked": path = "/large-chunked"
case "timeout", "cancel": path = "/slow"
case "setCookie": path = "/set-cookie"
case "checkCookie": path = "/check-cookie"
default: path = mode
}
var headers = ["Accept": "application/json", "Cache-Control": "no-store", "Authorization": "Bearer synthetic-probe"]
if mode == "invalidHeader" { headers["X-Bad\r\nHeader"] = "value" }
let isRevoke = mode.hasPrefix("revoke")
let request: [String: Any] = [
    "baseUrl": base,
    "path": path,
    "method": mode == "requestTooLarge" || mode == "requestAtLimit" ? "POST" : (isRevoke ? "DELETE" : "GET"),
    "headersJson": try! String(data: JSONSerialization.data(withJSONObject: headers), encoding: .utf8)!,
    "body": mode == "requestTooLarge" ? String(repeating: "x", count: 4 * 1024 * 1024 + 1) :
        mode == "requestAtLimit" ? String(repeating: "x", count: 4 * 1024 * 1024) : (mode == "revokeBody" ? "unexpected" : ""),
    "timeoutMs": timeoutMs
]
let json = String(data: try! JSONSerialization.data(withJSONObject: request), encoding: .utf8)!
let state = ProbeState()
let handle = NeraeHttpsBridge.request(json) { state.receive($0) }
if mode == "cancel" || mode == "revokeCancel" {
    DispatchQueue.global().asyncAfter(deadline: .now() + 0.1) { NeraeHttpsBridge.cancel(handle) }
}
let (count, result) = state.wait(seconds: Double(timeoutMs) / 1_000.0 + 4)
if mode == "revokeCancel" {
    Thread.sleep(forTimeInterval: 1.8)
    guard state.callbackCount() == 1 else { fputs("late cancellation callback was delivered\n", stderr); exit(2) }
}
guard count == 1, let data = result.data(using: .utf8),
      let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
    fputs("callback count/result invalid\n", stderr); exit(2)
}
guard let typed = try? JSONDecoder().decode(WireResult.self, from: data),
      typed.ok == (typed.code.isEmpty),
      (typed.ok && typed.status >= 100 && typed.status <= 599 && (try? JSONSerialization.jsonObject(with: Data(typed.headersJson.utf8)) as? [String: String]) != nil) ||
      (!typed.ok && typed.status == 0 && typed.headersJson == "{}" && typed.body.isEmpty) else {
    fputs("typed wire result decode/shape invalid\n", stderr); exit(2)
}
print(String(data: try! JSONSerialization.data(withJSONObject: ["callbackCount": count, "result": object], options: [.sortedKeys]), encoding: .utf8)!)
