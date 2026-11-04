import Foundation

/// Read-only integration of the protected journal, HTTPS replay and response
/// validator. The result is only a historical candidate, not authority to
/// modify a draft or settle an operation. A caller must recheck the app's
/// current session generation and establish crash-safe settlement separately.
final class NeraeRecoveryInspector {
    private let journal: NeraeVersionedTrainingJournal
    private let transport: NeraeRecoveryHTTPTransport

    init(journal: NeraeVersionedTrainingJournal, transport: NeraeRecoveryHTTPTransport) {
        self.journal = journal
        self.transport = transport
    }

    func inspect(operationKey: String, subject: String,
                 completion: @escaping (Result<NeraeRecoveryReplayResult, NeraeRecoveryHTTPError>) -> Void) {
        let intent: NeraeRecoveryIntent
        do { intent = try journal.recoveryIntent(operationKey: operationKey, subject: subject) }
        catch { completion(.failure(.unavailable)); return }

        transport.replay(intent) { [journal] result in
            guard case let .success(response) = result,
                  let body = String(data: response.body, encoding: .utf8),
                  let candidate = NeraeRecoveryReplayValidator.validate(
                    intent: intent, expectedSubjectId: subject, status: response.status,
                    headers: response.headers.map { NeraeRecoveryHeaderField(name: $0.key, value: $0.value) },
                    body: body),
                  let latest = try? journal.recoveryIntent(operationKey: operationKey, subject: subject),
                  Self.sameIntent(intent, latest) else {
                completion(.failure(.unavailable)); return
            }
            completion(.success(candidate))
        }
    }

    private static func sameIntent(_ first: NeraeRecoveryIntent, _ second: NeraeRecoveryIntent) -> Bool {
        let lhs = [first.revision, first.subjectId, first.clientEventId, first.operationKey,
                   first.path, first.idempotencyKey, first.body]
        let rhs = [second.revision, second.subjectId, second.clientEventId, second.operationKey,
                   second.path, second.idempotencyKey, second.body]
        return zip(lhs, rhs).allSatisfy { $0.utf8.elementsEqual($1.utf8) }
    }
}
