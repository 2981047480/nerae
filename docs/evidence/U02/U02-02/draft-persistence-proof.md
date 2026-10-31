# Draft persistence proof (process-local foundation)

## Scope

`DraftController.persistWithProof()` returns an opaque handle only after its exact serialized draft snapshot has been written successfully, the operation remains the controller's newest revision, and the captured authenticated subject/generation is still current. The handle is held in a process-local `WeakMap`; JSON serialization contains no proof material. `isCurrentPersistenceProof()` checks controller identity, subject, generation, revision, current auth lease, saved status, and byte-for-byte-equivalent JSON serialization of the live draft.

Any later `update()` or `persist()` invalidates an earlier handle before starting. A process-wide write revision also makes a newer write from another controller invalidate an earlier handle immediately; the revision is rechecked after an awaited proof write, so an older write cannot return a non-null but already-invalid handle. For bounded state, this conservatively revokes proofs for other drafts in the same runtime too. If another write supersedes proof issuance, the proof write returns null and leaves status `not_saved`; ordinary `persist()` and `update()` keep their original per-controller boolean behavior even when another key writes concurrently. The `draft` getter returns a copy, so external mutation cannot silently change controller state. `update()` snapshots the original event ID and timezone before invoking the transform, rejecting even in-place attempts to change either. Failed writes, writes whose acknowledgement is uncertain, superseded concurrent writes, and account/session changes cannot produce or retain a valid proof.

## Verification

The focused Node test files cover successful validation, opaque serialization, cross-controller rejection, getter-copy isolation, in-place identity mutation rejection, session generation change, invalidation by normal update/persist, write-then-error behavior, same/different-key overlaps, and ordinary-save compatibility.

## Limits

This is evidence of one successful write within one JavaScript runtime and the captured login generation. It is not durable across process restart, does not prove native storage durability after process death, and does not provide atomicity with the training operation journal or server snapshot. It must not be used as a journal-clear permission until a higher-level coordinator safely sequences both stores and revalidates the exact request/session binding.
