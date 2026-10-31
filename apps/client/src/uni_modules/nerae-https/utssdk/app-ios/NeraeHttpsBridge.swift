import Foundation
#if NERAE_HTTPS_TESTING
import Security
#endif

@objc(NeraeHttpsBridge)
public final class NeraeHttpsBridge: NSObject {
    private static let registryLock = NSLock()
    private static var active: [String: HttpsRequestOperation] = [:]
    private static let maxBodyBytes = 4 * 1024 * 1024
    private static let maxConcurrentRequests = 8
    fileprivate static let responseHeaderNames: Set<String> = [
        "content-type", "cache-control", "x-request-id", "idempotency-key-expires-at", "idempotency-replayed"
    ]
    #if NERAE_HTTPS_TESTING
    private static var testAnchor: Data?
    public static func setTestAnchor(_ data: Data?) { testAnchor = data }
    #endif

    /// Returns a local cancellation handle immediately. The JSON payload carries only
    /// request data; every completion is a fixed, bounded result delivered once.
    @objc public static func request(_ json: String, _ completion: @escaping (String) -> Void) -> String {
        let requestId = UUID().uuidString.lowercased()
        guard let request = HttpsRequestSpec.parse(json, maxBodyBytes: maxBodyBytes) else {
            DispatchQueue.main.async { completion(HttpsWire.failure(requestId, "invalid_request")) }
            return requestId
        }
        guard let url = request.url else {
            DispatchQueue.main.async { completion(HttpsWire.failure(requestId, "invalid_configuration")) }
            return requestId
        }
        let operation = HttpsRequestOperation(id: requestId, request: request, url: url, completion: completion)
        registryLock.lock()
        guard active.count < maxConcurrentRequests else {
            registryLock.unlock()
            DispatchQueue.main.async { completion(HttpsWire.failure(requestId, "unavailable")) }
            return requestId
        }
        active[requestId] = operation
        registryLock.unlock()
        operation.start()
        return requestId
    }

    @objc public static func cancel(_ requestId: String) {
        registryLock.lock()
        let operation = active[requestId]
        registryLock.unlock()
        operation?.finish(error: "cancelled")
    }

    fileprivate static func remove(_ requestId: String) {
        registryLock.lock()
        active.removeValue(forKey: requestId)
        registryLock.unlock()
    }

    #if NERAE_HTTPS_TESTING
    fileprivate static func applyTestTrust(_ challenge: URLAuthenticationChallenge,
                                          completion: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) -> Bool {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let trust = challenge.protectionSpace.serverTrust,
              let anchorData = testAnchor,
              let certificate = SecCertificateCreateWithData(nil, anchorData as CFData) else { return false }
        let policy = SecPolicyCreateSSL(true, challenge.protectionSpace.host as CFString)
        SecTrustSetPolicies(trust, policy)
        SecTrustSetAnchorCertificates(trust, [certificate] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true)
        guard SecTrustEvaluateWithError(trust, nil) else {
            completion(.performDefaultHandling, nil)
            return true
        }
        completion(.useCredential, URLCredential(trust: trust))
        return true
    }
    #endif
}

private struct HttpsRequestSpec {
    let method: String
    let headers: [String: String]
    let body: Data
    let timeout: TimeInterval
    let url: URL?

    static func parse(_ text: String, maxBodyBytes: Int) -> HttpsRequestSpec? {
        guard let raw = text.data(using: .utf8), raw.count <= 9 * 1024 * 1024,
              let object = try? JSONSerialization.jsonObject(with: raw),
              let value = object as? [String: Any],
              let base = value["baseUrl"] as? String,
              let path = value["path"] as? String,
              let method = value["method"] as? String,
              let headersText = value["headersJson"] as? String,
              let bodyText = value["body"] as? String,
              let timeoutMs = value["timeoutMs"] as? Int,
              method == "GET" || method == "POST" || method == "DELETE",
              (1_000...120_000).contains(timeoutMs),
              let body = bodyText.data(using: .utf8), body.count <= maxBodyBytes,
              (method != "GET" && method != "DELETE") || body.isEmpty,
              let headersData = headersText.data(using: .utf8), headersData.count <= 20 * 1024,
              let headersObject = try? JSONSerialization.jsonObject(with: headersData),
              let rawHeaders = headersObject as? [String: Any],
              base.utf8.count <= 2_048, validRoute(path), path.utf8.count <= 8_192 else { return nil }

        let allowed: Set<String> = ["accept", "authorization", "cache-control", "content-type",
                                    "idempotency-key", "x-request-id"]
        var headers: [String: String] = [:]
        var names = Set<String>()
        var size = 0
        for (name, rawValue) in rawHeaders {
            guard let headerValue = rawValue as? String else { return nil }
            let lower = name.lowercased()
            guard allowed.contains(lower), names.insert(lower).inserted,
                  !name.isEmpty, name.utf8.count <= 128,
                  !name.unicodeScalars.contains(where: { $0.value <= 32 || $0.value >= 127 || $0 == ":" }),
                  !headerValue.unicodeScalars.contains(where: { $0.value < 32 || $0.value == 127 }),
                  headerValue.utf8.count <= 8 * 1024 else { return nil }
            size += name.utf8.count + headerValue.utf8.count
            guard size <= 16 * 1024 else { return nil }
            headers[name] = headerValue
        }
        return HttpsRequestSpec(method: method, headers: headers, body: body,
                                timeout: TimeInterval(timeoutMs) / 1_000.0, url: buildURL(base: base, path: path))
    }

    private static func buildURL(base: String, path: String) -> URL? {
        guard !base.isEmpty, !path.isEmpty,
              !path.contains("#"), !path.contains("\\"), !containsControl(path),
              path.hasPrefix("/"), !path.hasPrefix("//") else { return nil }
        guard let baseComponents = URLComponents(string: base),
              baseComponents.scheme?.lowercased() == "https",
              let host = baseComponents.host, !host.isEmpty,
              baseComponents.user == nil, baseComponents.password == nil,
              baseComponents.query == nil, baseComponents.fragment == nil,
              baseComponents.port.map({ (1...65_535).contains($0) }) ?? true,
              validPath(baseComponents.percentEncodedPath),
              validRoute(path) else { return nil }

        let routeParts = path.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        let routePath = String(routeParts[0])
        let query = routeParts.count == 2 ? String(routeParts[1]) : nil
        guard query.map(validQuery) ?? true else { return nil }

        var target = baseComponents
        let prefix = baseComponents.percentEncodedPath.hasSuffix("/")
            ? String(baseComponents.percentEncodedPath.dropLast()) : baseComponents.percentEncodedPath
        target.percentEncodedPath = prefix + routePath
        target.percentEncodedQuery = query
        target.fragment = nil
        guard let url = target.url, url.scheme?.lowercased() == "https",
              url.host?.lowercased() == host.lowercased(), url.port == baseComponents.port,
              url.user == nil, url.password == nil, url.fragment == nil,
              url.path.hasPrefix((baseComponents.path.hasSuffix("/") ? String(baseComponents.path.dropLast()) : baseComponents.path) + "/") else {
            return nil
        }
        return url
    }

    private static func validRoute(_ value: String) -> Bool {
        let parts = value.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        guard validPath(String(parts[0])) else { return false }
        return parts.count < 2 || validQuery(String(parts[1]))
    }

    private static func validPath(_ encodedPath: String) -> Bool {
        guard encodedPath.isEmpty || encodedPath.hasPrefix("/"),
              !encodedPath.lowercased().contains("%2f"), !encodedPath.lowercased().contains("%5c"),
              !encodedPath.lowercased().contains("%25"),
              validPercentEncoded(encodedPath, allowSlash: true, allowQuestion: false),
              let decoded = encodedPath.removingPercentEncoding,
              !decoded.contains("\\"), !containsControl(decoded) else { return false }
        return !decoded.split(separator: "/", omittingEmptySubsequences: false).contains { $0 == "." || $0 == ".." }
    }

    private static func validQuery(_ value: String) -> Bool {
        validPercentEncoded(value, allowSlash: true, allowQuestion: true)
    }

    /// URLComponents.percentEncodedPath/query setters require RFC 3986 characters
    /// and complete percent escapes; they can raise a fatal exception otherwise.
    private static func validPercentEncoded(_ value: String, allowSlash: Bool, allowQuestion: Bool) -> Bool {
        let bytes = Array(value.utf8)
        var index = 0
        while index < bytes.count {
            let byte = bytes[index]
            if byte == 37 {
                guard index + 2 < bytes.count,
                      isHex(bytes[index + 1]), isHex(bytes[index + 2]) else { return false }
                index += 3
                continue
            }
            guard isUnreserved(byte) || isSubDelimiter(byte) || byte == 58 || byte == 64 ||
                  (allowSlash && byte == 47) || (allowQuestion && byte == 63) else { return false }
            index += 1
        }
        return true
    }

    private static func isUnreserved(_ byte: UInt8) -> Bool {
        (65...90).contains(byte) || (97...122).contains(byte) || (48...57).contains(byte) ||
            byte == 45 || byte == 46 || byte == 95 || byte == 126
    }

    private static func isSubDelimiter(_ byte: UInt8) -> Bool {
        byte == 33 || byte == 36 || byte == 38 || byte == 39 || byte == 40 || byte == 41 ||
            byte == 42 || byte == 43 || byte == 44 || byte == 59 || byte == 61
    }

    private static func isHex(_ value: UInt8) -> Bool {
        (48...57).contains(value) || (65...70).contains(value) || (97...102).contains(value)
    }

    private static func containsControl(_ value: String) -> Bool {
        value.unicodeScalars.contains { $0.value < 32 || $0.value == 127 }
    }
}

private final class HttpsRequestOperation: NSObject, URLSessionDataDelegate, URLSessionTaskDelegate {
    private let id: String
    private let spec: HttpsRequestSpec
    private let url: URL
    private let completion: (String) -> Void
    private let lock = NSLock()
    private var settled = false
    private var received = Data()
    private var response: HTTPURLResponse?
    private var session: URLSession?
    private var task: URLSessionDataTask?
    private var timeoutWork: DispatchWorkItem?

    init(id: String, request: HttpsRequestSpec, url: URL, completion: @escaping (String) -> Void) {
        self.id = id
        self.spec = request
        self.url = url
        self.completion = completion
    }

    func start() {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.urlCache = nil
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.urlCredentialStorage = nil
        configuration.timeoutIntervalForRequest = spec.timeout
        configuration.timeoutIntervalForResource = spec.timeout
        let queue = OperationQueue()
        queue.maxConcurrentOperationCount = 1
        let session = URLSession(configuration: configuration, delegate: self, delegateQueue: queue)
        self.session = session

        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: spec.timeout)
        request.httpMethod = spec.method
        request.httpShouldHandleCookies = false
        request.setValue("identity", forHTTPHeaderField: "Accept-Encoding")
        request.httpBody = spec.body.isEmpty ? nil : spec.body
        for (name, value) in spec.headers { request.setValue(value, forHTTPHeaderField: name) }
        // The caller cannot opt into caching by supplying a different allowed value.
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")
        let task = session.dataTask(with: request)
        self.task = task
        let timeout = DispatchWorkItem { [weak self] in self?.finish(error: "timeout") }
        timeoutWork = timeout
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + spec.timeout, execute: timeout)
        task.resume()
    }

    func cancel() { finish(error: "cancelled") }

    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask,
                    didReceive response: URLResponse,
                    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        guard let http = response as? HTTPURLResponse else {
            completionHandler(.cancel)
            finish(error: "invalid_response")
            return
        }
        if http.expectedContentLength > Int64(HttpsRequestSpecLimit.responseBytes) {
            completionHandler(.cancel)
            finish(error: "response_too_large")
            return
        }
        if http.statusCode == 204 && !Self.hasValidEmpty204Framing(http) {
            completionHandler(.cancel)
            finish(error: "invalid_response")
            return
        }
        lock.lock()
        self.response = http
        lock.unlock()
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        guard !settled else { lock.unlock(); return }
        if received.count > HttpsRequestSpecLimit.responseBytes - data.count {
            lock.unlock()
            finish(error: "response_too_large")
            return
        }
        received.append(data)
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if let error {
            let ns = error as NSError
            finish(error: ns.domain == NSURLErrorDomain && ns.code == NSURLErrorTimedOut ? "timeout" :
                   ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled ? "cancelled" : "network")
            return
        }
        lock.lock()
        let http = response
        let body = received
        lock.unlock()
        guard let http, let bodyText = String(data: body, encoding: .utf8) else {
            finish(error: "invalid_response")
            return
        }
        if http.statusCode == 204 && !body.isEmpty {
            finish(error: "invalid_response")
            return
        }
        guard let safeHeaders = Self.safeHeaders(http) else {
            finish(error: "invalid_response")
            return
        }
        finish(response: HttpsWire.success(id: id, status: http.statusCode,
                                           headers: safeHeaders, body: bodyText))
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask,
                    willCacheResponse proposedResponse: CachedURLResponse,
                    completionHandler: @escaping (CachedURLResponse?) -> Void) {
        completionHandler(nil)
    }

    #if NERAE_HTTPS_TESTING
    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        if NeraeHttpsBridge.applyTestTrust(challenge, completion: completionHandler) { return }
        completionHandler(.performDefaultHandling, nil)
    }
    #endif

    private static func safeHeaders(_ response: HTTPURLResponse) -> [String: String]? {
        var result: [String: String] = [:]
        var size = 0
        for (rawName, rawValue) in response.allHeaderFields {
            guard let name = rawName as? String else { continue }
            let lowerName = name.lowercased()
            guard NeraeHttpsBridge.responseHeaderNames.contains(lowerName) else { continue }
            guard let value = rawValue as? String,
                  !value.unicodeScalars.contains(where: { $0.value < 32 || $0.value == 127 }) else { return nil }
            guard result[lowerName] == nil else { return nil }
            if lowerName == "idempotency-replayed" && value != "true" { return nil }
            size += name.utf8.count + value.utf8.count
            guard size <= 8 * 1024 else { return nil }
            result[lowerName] = value
        }
        return result
    }

    private static func hasValidEmpty204Framing(_ response: HTTPURLResponse) -> Bool {
        for rawName in response.allHeaderFields.keys {
            guard let name = rawName as? String else { return false }
            let lower = name.lowercased()
            if lower == "transfer-encoding" { return false }
            if lower == "content-length" { return false }
        }
        return response.expectedContentLength <= 0
    }

    fileprivate func finish(response result: String? = nil, error: String? = nil) {
        lock.lock()
        guard !settled else { lock.unlock(); return }
        settled = true
        timeoutWork?.cancel()
        timeoutWork = nil
        let task = self.task
        let session = self.session
        self.task = nil
        self.session = nil
        lock.unlock()

        NeraeHttpsBridge.remove(id)
        if error != nil { task?.cancel() }
        if error != nil { session?.invalidateAndCancel() } else { session?.finishTasksAndInvalidate() }
        let payload = result ?? HttpsWire.failure(id, error ?? "network")
        DispatchQueue.main.async { self.completion(payload) }
    }
}

private enum HttpsRequestSpecLimit { static let responseBytes = 4 * 1024 * 1024 }

private enum HttpsWire {
    static func success(id: String, status: Int, headers: [String: String], body: String) -> String {
        guard let headerData = try? JSONSerialization.data(withJSONObject: headers, options: [.sortedKeys]),
              let headersJson = String(data: headerData, encoding: .utf8) else {
            return failure(id, "invalid_response")
        }
        return encode(["ok": true, "requestId": id, "status": status, "headersJson": headersJson, "body": body, "code": ""])
    }
    static func failure(_ id: String, _ code: String) -> String {
        encode(["ok": false, "requestId": id, "status": 0, "headersJson": "{}", "body": "", "code": code])
    }
    private static func encode(_ value: [String: Any]) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]),
              let text = String(data: data, encoding: .utf8) else {
            return "{\"ok\":false,\"requestId\":\"\",\"status\":0,\"headersJson\":\"{}\",\"body\":\"\",\"code\":\"invalid_response\"}"
        }
        return text
    }
}
