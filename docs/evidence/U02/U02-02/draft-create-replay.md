# Draft free-session replay candidate

The recording adapter can query the read-only replay endpoint for an exact,
already-dispatched free-session request recovered from a draft. It validates
the current account, event, state, key and canonical body before querying.
The result is explicitly a historical candidate, not a current session
snapshot or permission to clear the draft. A caller must re-read the protected
draft, use a revision-bound CAS and fetch the current server session.

On 2026-11-05 the recording API and replay tests passed 62 cases. Independent
adversarial checks for mutable response fields and invalid Unicode passed after
the response was materialized once before validation. The existing journal
path still re-reads and compares its pending operation after the HTTP call.
Client typechecking passed. These tests use synthetic transport responses;
the page, native draft coordinator, deployed service and device are not connected.
