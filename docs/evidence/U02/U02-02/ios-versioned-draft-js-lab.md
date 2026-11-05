# iOS versioned draft adapter: isolated candidate

A JavaScript adapter and controller candidate lives at
`/private/tmp/nerae-versioned-draft-adapter`. It uses the existing iOS native
draft revision and exact-value comparison; it is not connected to the App
editor. The independently reviewed snapshot is retained under
`/private/tmp/nerae-draft-proof-review.h61DFALi`.

The candidate's 22 synthetic tests and isolated strict TypeScript check
passed. Independent review reproduced and then rechecked fixes for stale
predecessor revisions, independent failed drafts, a split from the same failed
snapshot, and proof issuance while another failed branch remains. No proof is
issued or remains current while an unresolved branch for the same key exists.

An isolated project-layout port at
`/private/tmp/nerae-adapter-integrated.20261104-ws/apps/client` selects the
draft adapter from the compiled native capability. The 22 cases were added to
that port's test suite; 310 relevant synthetic Node tests passed. Project
typechecking and 5.33 App/Weixin resource builds passed before those test-only
additions. The port remains unmerged; the failed initial checks and precise
commands are retained in
`/private/tmp/nerae-adapter-integrated.20261104.verification.md`.

Integration is blocked: ordinary editor updates can still report their own
write as locally saved while a different failed branch is unresolved. The
current page cannot present and resolve that conflict. Failed snapshots also
live only in JavaScript memory and can be lost if the process exits before
they reach protected storage. A user-visible recovery flow and an appropriate
durability boundary are needed before this candidate is enabled. The
candidate still needs repository import/platform-factory adaptation, project
regression tests and actual Keychain/device verification.
