# Journal receipt settlement: ABA review

The journal currently uses compare-and-swap on stored bytes, with `revision` inside those same bytes. A separate, unmerged receipt-credential candidate re-read the bytes and compared their embedded revision. Independent review constructed a sequence in which a holder of the storage CAS interface changes `v1 → v2 → original v1`; the candidate then incorrectly considered the old snapshot current. The isolated regression exited 1 as expected. The candidate was **not integrated** and no journal entry was cleared.

Future settlement must provide an atomic mutation boundary that prevents a stale operation from being accepted after deletion/recreation or restoration of older bytes, and must define the scope of that guarantee across app processes, device reboot and storage rollback. A revision stored only in caller-replaceable value bytes is insufficient. Native iOS/Android storage support and migration need to be designed and tested before treating a verified receipt as authorization to clear a pending operation.

This finding does not invalidate the existing read-only replay query: it never clears or rewrites the pending request. The app still lacks automatic receipt settlement and crash-safe reconciliation of drafts.
