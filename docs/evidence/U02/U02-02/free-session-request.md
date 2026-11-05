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
