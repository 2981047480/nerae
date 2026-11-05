# Free-session request preparation

`RecordingApi.createFreeSession` now prepares an immutable method, path,
idempotency key and JSON body, then sends that exact body through its existing
authenticated HTTP path. A prepared object is bound to the same API instance
and is consumed once; it is not a persisted operation or a recovery permit.

On 2026-11-05 the integrated `recordingApi.test.mjs` suite passed 46 tests,
including Unicode, request-body identity, invalid key, forged object and
session-change cases. Client TypeScript checking passed. Durable request
storage, post-restart recovery, the page save action and server synchronization
remain unfinished.

## 2026-11-05 creation response binding

A direct creation response now must agree with the frozen request's event,
source, date and timezone. An invalid response is reconciliation-required,
not a successful create. A validated response is associated with the exact
prepared object only in this JS runtime; that association is neither a durable
receipt nor authorization to settle. The caller must recheck the authenticated
lease and the persisted operation before changing a draft. A session/token
change between parsing and binding is rejected.

The recording API and replay suites passed 67 synthetic-transport tests; client
typechecking passed. Page dispatch, durable settlement, server deployment and
real devices remain unverified.
