import Foundation
import CoreFoundation
#if NERAE_RECOVERY_HTTP_TESTING
import Security
#endif

/// Read-only replay transport. A result is not a server-receipt authorization:
/// a caller must validate its content and recheck the journal before settlement.
struct NeraeRecoveryHTTPResponse: CustomStringConvertible, CustomDebugStringConvertible {
    let status: Int
    let headers: [String: String]
    let body: Data
    var description: String { "NeraeRecoveryHTTPResponse{redacted}" }
    var debugDescription: String { description }
}

enum NeraeRecoveryHTTPError: Error {
    case unavailable
}

/// The base address is loaded from the signed application's configuration.
/// JavaScript callers cannot select a different destination for this request.
struct NeraeRecoveryHTTPConfiguration {
    private let base: URLComponents

    private init?(_ raw: String) {
        guard !raw.isEmpty, raw.utf8.count <= 2048,
              !raw.unicodeScalars.contains(where: { $0.value <= 32 || $0.value == 127 || $0 == "\\" }),
              let parsed = URLComponents(string: raw), parsed.scheme?.lowercased() == "https",
              let host = parsed.host, !host.isEmpty, parsed.user == nil, parsed.password == nil,
              parsed.query == nil, parsed.fragment == nil,
              parsed.port.map({ (1...65_535).contains($0) }) ?? true,
              Self.validBasePath(parsed.percentEncodedPath) else { return nil }
        base = parsed
    }

    static func fromAppBundle(_ bundle: Bundle = .main) -> NeraeRecoveryHTTPConfiguration? {
        guard let value = bundle.object(forInfoDictionaryKey: "NeraeAPIBaseURL") as? String else { return nil }
        return NeraeRecoveryHTTPConfiguration(value)
    }

    #if NERAE_RECOVERY_HTTP_TESTING
    static func forTesting(_ raw: String) -> NeraeRecoveryHTTPConfiguration? { NeraeRecoveryHTTPConfiguration(raw) }
    #endif

    fileprivate func replayURL(path: String) -> URL? {
        guard Self.validWorkoutPath(path) else { return nil }
        var target = base
        let prefix = base.percentEncodedPath.replacingOccurrences(of: "/+$", with: "", options: .regularExpression)
        target.percentEncodedPath = prefix + path + "/replay"
        guard let url = target.url, url.scheme?.lowercased() == "https",
              url.host?.lowercased() == base.host?.lowercased(), url.port == base.port,
              url.user == nil, url.password == nil, url.query == nil, url.fragment == nil else { return nil }
        return url
    }

    private static func validBasePath(_ path: String) -> Bool {
        guard path.isEmpty || path.hasPrefix("/"), !path.contains("//"),
              !path.lowercased().contains("%2f"), !path.lowercased().contains("%5c"),
              !path.lowercased().contains("%25"), let decoded = path.removingPercentEncoding,
              !decoded.contains("\\"), !decoded.unicodeScalars.contains(where: { $0.value <= 32 || $0.value == 127 }) else { return false }
        return !decoded.split(separator: "/", omittingEmptySubsequences: false).contains { $0 == "." || $0 == ".." }
    }

    private static func validWorkoutPath(_ path: String) -> Bool {
        if path == "/me/sessions" || path == "/me/recordings" { return true }
        guard let match = path.range(of: #"\A/me/(?:sessions|recordings)/[1-9][0-9]{0,19}/(?:sets|finish)\z"#, options: .regularExpression) else { return false }
        return match.lowerBound == path.startIndex && match.upperBound == path.endIndex
    }
}

private struct NeraeRecoveryCredential {
    let subject: String
    let token: String
    let snapshot: NeraeAuthenticationStateSnapshot
}

/// Parses a snapshot obtained from the app's protected authentication gate.
private func recoveryCredential(_ snapshot: NeraeAuthenticationStateSnapshot) throws -> NeraeRecoveryCredential {
    guard snapshot.found, NeraeStrictJSON.isCanonicalJournalObject(snapshot.encodedState),
          let data = snapshot.encodedState.data(using: .utf8),
          let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          Set(object.keys) == Set(["schema", "subjectId", "accessToken"]),
          let schema = object["schema"] as? NSNumber, CFGetTypeID(schema) != CFBooleanGetTypeID(),
          schema.intValue == 1, schema.doubleValue == 1,
          let subject = object["subjectId"] as? String, (1...20).contains(subject.utf8.count),
          subject.utf8.first != 48, subject.utf8.allSatisfy({ (48...57).contains($0) }),
          let token = object["accessToken"] as? String, (1...8192).contains(token.utf8.count),
          token.utf8.allSatisfy({ (33...126).contains($0) }) else { throw NeraeRecoveryHTTPError.unavailable }
    return NeraeRecoveryCredential(subject: subject, token: token, snapshot: snapshot)
}

final class NeraeRecoveryHTTPTransport {
    private static let registryLock = NSLock()
    private static var active: [UUID: NeraeRecoveryHTTPRequest] = [:]
    private static let maxConcurrent = 8
    private let configuration: NeraeRecoveryHTTPConfiguration
    private let authentication: NeraeAuthenticationStateGenerationGate
    #if NERAE_RECOVERY_HTTP_TESTING
    fileprivate static var testAnchor: Data?
    static func setTestAnchor(_ value: Data?) { testAnchor = value }
    #endif

    init(configuration: NeraeRecoveryHTTPConfiguration, authentication: NeraeAuthenticationStateGenerationGate) {
        self.configuration = configuration
        self.authentication = authentication
    }

    /// The caller must obtain intent from the native journal, not from JS.
    /// The exact saved body and idempotency key are sent only to the replay
    /// endpoint; this method never sends an ordinary mutating request.
    func replay(_ intent: NeraeRecoveryIntent, completion: @escaping (Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>) -> Void) {
        do {
            let credential = try recoveryCredential(authentication.snapshot())
            guard credential.subject == intent.subjectId,
                  let url = configuration.replayURL(path: intent.path),
                  (8...128).contains(intent.idempotencyKey.utf8.count),
                  intent.idempotencyKey.utf8.allSatisfy({ (48...57).contains($0) || (65...90).contains($0) ||
                      (97...122).contains($0) || [45, 46, 95, 126].contains($0) }),
                  let body = intent.body.data(using: .utf8), !body.isEmpty, body.count <= 64 * 1024,
                  NeraeStrictJSON.isCanonicalJournalObject(intent.body) else { throw NeraeRecoveryHTTPError.unavailable }
            let id = UUID()
            let operation = NeraeRecoveryHTTPRequest(id: id, url: url, body: body, key: intent.idempotencyKey,
                credential: credential, authentication: authentication, completion: completion)
            Self.registryLock.lock()
            guard Self.active.count < Self.maxConcurrent else {
                Self.registryLock.unlock()
                throw NeraeRecoveryHTTPError.unavailable
            }
            Self.active[id] = operation
            Self.registryLock.unlock()
            operation.start()
        } catch {
            completion(.failure(.unavailable))
        }
    }

    fileprivate static func remove(_ id: UUID) {
        registryLock.lock()
        active.removeValue(forKey: id)
        registryLock.unlock()
    }
}

private final class NeraeRecoveryHTTPRequest: NSObject, URLSessionDataDelegate, URLSessionTaskDelegate, @unchecked Sendable {
    private static let responseLimit = 4 * 1024 * 1024
    private static let allowedHeaders: Set<String> = ["content-type", "cache-control", "x-request-id", "idempotency-key-expires-at", "idempotency-replayed"]
    private let id: UUID
    private let url: URL
    private let body: Data
    private let key: String
    private let credential: NeraeRecoveryCredential
    private let authentication: NeraeAuthenticationStateGenerationGate
    private let completion: (Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>) -> Void
    private let lock = NSLock()
    private var finished = false
    private var received = Data()
    private var response: HTTPURLResponse?
    private var session: URLSession?
    private var timeout: DispatchWorkItem?

    init(id: UUID, url: URL, body: Data, key: String, credential: NeraeRecoveryCredential,
         authentication: NeraeAuthenticationStateGenerationGate,
         completion: @escaping (Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>) -> Void) {
        self.id = id; self.url = url; self.body = body; self.key = key
        self.credential = credential; self.authentication = authentication; self.completion = completion
    }

    func start() {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.urlCache = nil
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.urlCredentialStorage = nil
        configuration.timeoutIntervalForRequest = 30
        configuration.timeoutIntervalForResource = 30
        let queue = OperationQueue()
        queue.maxConcurrentOperationCount = 1
        let session = URLSession(configuration: configuration, delegate: self, delegateQueue: queue)
        self.session = session
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 30)
        request.httpMethod = "POST"
        request.httpShouldHandleCookies = false
        request.httpBody = body
        request.setValue("Bearer \(credential.token)", forHTTPHeaderField: "Authorization")
        request.setValue(key, forHTTPHeaderField: "Idempotency-Key")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")
        request.setValue("identity", forHTTPHeaderField: "Accept-Encoding")
        let work = DispatchWorkItem { [weak self] in self?.finish(.failure(.unavailable)) }
        timeout = work
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 30, execute: work)
        session.dataTask(with: request).resume()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }

    #if NERAE_RECOVERY_HTTP_TESTING
    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let trust = challenge.protectionSpace.serverTrust,
              let data = NeraeRecoveryHTTPTransport.testAnchor,
              let certificate = SecCertificateCreateWithData(nil, data as CFData) else {
            completionHandler(.performDefaultHandling, nil); return
        }
        SecTrustSetPolicies(trust, SecPolicyCreateSSL(true, challenge.protectionSpace.host as CFString))
        SecTrustSetAnchorCertificates(trust, [certificate] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true)
        guard SecTrustEvaluateWithError(trust, nil) else { completionHandler(.cancelAuthenticationChallenge, nil); return }
        completionHandler(.useCredential, URLCredential(trust: trust))
    }
    #endif

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
                    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        guard let http = response as? HTTPURLResponse, http.expectedContentLength <= Int64(Self.responseLimit) else {
            completionHandler(.cancel); finish(.failure(.unavailable)); return
        }
        lock.lock(); self.response = http; lock.unlock()
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        guard !finished else { lock.unlock(); return }
        guard data.count <= Self.responseLimit - received.count else {
            lock.unlock(); finish(.failure(.unavailable)); return
        }
        received.append(data)
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard error == nil else { finish(.failure(.unavailable)); return }
        lock.lock(); let http = response; let responseBody = received; lock.unlock()
        guard let http, (200...299).contains(http.statusCode),
              let headers = Self.safeHeaders(http), String(data: responseBody, encoding: .utf8) != nil,
              authentication.isCurrent(credential.snapshot) else {
            finish(.failure(.unavailable)); return
        }
        finish(.success(NeraeRecoveryHTTPResponse(status: http.statusCode, headers: headers, body: responseBody)))
    }

    private static func safeHeaders(_ response: HTTPURLResponse) -> [String: String]? {
        var output: [String: String] = [:]
        var size = 0
        for (rawName, rawValue) in response.allHeaderFields {
            guard let name = rawName as? String else { continue }
            let lower = name.lowercased()
            guard allowedHeaders.contains(lower) else { continue }
            guard let value = rawValue as? String, output[lower] == nil,
                  !value.unicodeScalars.contains(where: { $0.value < 32 || $0.value == 127 }) else { return nil }
            size += name.utf8.count + value.utf8.count
            guard size <= 8192 else { return nil }
            output[lower] = value
        }
        return output
    }

    private func finish(_ result: Result<NeraeRecoveryHTTPResponse, NeraeRecoveryHTTPError>) {
        lock.lock()
        guard !finished else { lock.unlock(); return }
        finished = true
        lock.unlock()
        timeout?.cancel()
        session?.invalidateAndCancel()
        NeraeRecoveryHTTPTransport.remove(id)
        completion(result)
    }
}
