import Foundation

final class SyntheticAuthenticationBackend: NeraeVaultBackend {
    private let lock = NSLock()
    private var value: Data?
    func read() throws -> Data? { lock.lock(); defer { lock.unlock() }; return value }
    func write(_ value: Data) throws { lock.lock(); self.value = value; lock.unlock() }
    func clear() throws { lock.lock(); value = nil; lock.unlock() }
}

final class ProbeResult {
    private let lock = NSLock()
    private let signal = DispatchSemaphore(value: 0)
    private var result: Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>?
    private var count = 0
    func receive(_ result: Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>) {
        lock.lock(); count += 1; self.result = result; lock.unlock(); signal.signal()
    }
    func wait() -> Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError> {
        precondition(signal.wait(timeout: .now() + 10) == .success, "recovery callback timed out")
        Thread.sleep(forTimeInterval: 0.1)
        lock.lock(); defer { lock.unlock() }
        precondition(count == 1, "recovery callback fired more than once")
        return result!
    }
}

@main
struct RecoveryHTTPProbe {
    static func main() throws {
        let args = CommandLine.arguments
        precondition(args.count == 4)
        let port = args[1]
        let goodAnchor = try Data(contentsOf: URL(fileURLWithPath: args[2]))
        let badAnchor = try Data(contentsOf: URL(fileURLWithPath: args[3]))
        let base = "https://127.0.0.1:\(port)"
        for value in ["http://127.0.0.1:\(port)", "https://user:pass@127.0.0.1:\(port)",
                      "\(base)/%2e%2e", "\(base)/bad%2Fpath", "\(base)/path?query=1", "\(base)/bad#fragment"] {
            precondition(NeraeRecoveryHTTPConfiguration.forTesting(value) == nil, "unsafe service address accepted")
        }
        let backend = SyntheticAuthenticationBackend()
        let vault = NeraeCredentialVault(backend: backend)
        try vault.write(#"{"schema":1,"subjectId":"101","accessToken":"synthetic-token"}"#)
        let intent = NeraeRecoveryIntent(revision: "2", subjectId: "101", clientEventId: "event-0001",
            operationKey: "nerae.workout.operation.v1.101.event-0001", path: "/me/sessions", idempotencyKey: "create-key-001",
            body: #"{"source":"free","date":"2026-10-29","clientEventId":"event-0001"}"#)

        func send(_ prefix: String, _ request: NeraeRecoveryIntent = intent, mutate: (() -> Void)? = nil)
            -> Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError> {
            let config = NeraeRecoveryHTTPConfiguration.forTesting(base + prefix)!
            let probe = ProbeResult()
            NeraeRecoveryHTTPTransport(configuration: config, vault: vault).replay(request) { probe.receive($0) }
            if let mutate { DispatchQueue.global().asyncAfter(deadline: .now() + 0.2, execute: mutate) }
            return probe.wait()
        }
        func failed(_ result: Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>) -> Bool {
            if case .failure = result { return true }; return false
        }

        NeraeRecoveryHTTPTransport.setTestAnchor(nil)
        precondition(failed(send("/ok")), "untrusted certificate was accepted")
        NeraeRecoveryHTTPTransport.setTestAnchor(badAnchor)
        precondition(failed(send("/ok")), "wrong certificate authority was accepted")
        NeraeRecoveryHTTPTransport.setTestAnchor(goodAnchor)
        let wrongHost = NeraeRecoveryHTTPConfiguration.forTesting("https://localhost:\(port)/ok")!
        let wrongHostProbe = ProbeResult()
        NeraeRecoveryHTTPTransport(configuration: wrongHost, vault: vault).replay(intent) { wrongHostProbe.receive($0) }
        precondition(failed(wrongHostProbe.wait()), "certificate for a different host was accepted")
        guard case let .success(response) = send("/ok") else { fatalError("trusted replay request failed") }
        precondition(response.status == 201 && response.headers["idempotency-replayed"] == "true" &&
                     String(data: response.body, encoding: .utf8) == #"{"id":"42"}"#, "response mismatch")
        let wrongOwner = NeraeRecoveryIntent(revision: "2", subjectId: "202", clientEventId: intent.clientEventId,
            operationKey: intent.operationKey, path: intent.path, idempotencyKey: intent.idempotencyKey, body: intent.body)
        precondition(failed(send("/wrong-owner", wrongOwner)), "different account was allowed")
        let wrongPath = NeraeRecoveryIntent(revision: "2", subjectId: intent.subjectId, clientEventId: intent.clientEventId,
            operationKey: intent.operationKey, path: "/me/other", idempotencyKey: intent.idempotencyKey, body: intent.body)
        precondition(failed(send("/wrong-path", wrongPath)), "non-replay path was allowed")
        let newlinePath = NeraeRecoveryIntent(revision: "2", subjectId: intent.subjectId, clientEventId: intent.clientEventId,
            operationKey: intent.operationKey, path: "/me/sessions/1/sets\n", idempotencyKey: intent.idempotencyKey, body: intent.body)
        precondition(failed(send("/newline-path", newlinePath)), "path with trailing newline was allowed")
        try vault.write(#"{"schema":1,"subjectId":"101","accessToken":"synthetic-token","extra":true}"#)
        precondition(failed(send("/malformed-auth")), "unexpected authentication fields were allowed")
        try vault.write(#"{"schema":1,"subjectId":"101","accessToken":"synthetic-token"}"#)
        precondition(failed(send("/redirect")), "redirect was treated as a successful replay")
        precondition(failed(send("/large")), "oversized response was accepted")
        precondition(failed(send("/chunked")), "oversized chunked response was accepted")
        let changed = send("/slow", mutate: {
            try? vault.write(#"{"schema":1,"subjectId":"202","accessToken":"other-token"}"#)
        })
        precondition(failed(changed), "response after account change was released")
        NeraeRecoveryHTTPTransport.setTestAnchor(nil)
        print("PASS: native replay HTTPS, trusted certificate, exact request, redirect/size/owner/session guards")
    }
}
