# iOS local draft discovery candidate

The new native candidate enumerates the existing Keychain draft service rather
than maintaining a second index. For each item it validates the envelope and
the digest derived from its subject and event; only the requested subject's
event IDs are returned. The App adapter binds the request to the current
session and rechecks it before handing a result to the caller. The response
contains at most 100 event IDs and an optional cursor, ordered by UTF-8 bytes.
It also contains a separate device-level `hasUnclassifiedItems` flag: a
damaged or unreadable item whose owner cannot be proved is never attributed to
the current subject, discarded, or silently treated as absent. A Keychain
read failure returns unavailable rather than an empty success.

An independent review found that an earlier candidate's global 256-item and
8 MiB limits could make all drafts undiscoverable while creation remained
allowed. The candidate was changed to page results without those global
failure thresholds. Synthetic Swift tests now create 280 matching drafts and
recover all three pages, and scan more than 8 MiB of invalid items without
turning the list into an unavailable result. They also exercise different
subjects, wrong digests, damaged values and backend failure. Client tests cover
pagination, malformed responses and account changes. No stored item is deleted.

The lookup rescans the Keychain service for each page. There is no persistent
snapshot: a draft created ahead of an already-consumed cursor can appear only
on a fresh scan. The caller must complete pages and repeat from the beginning
after changes. Keychain returns all matching attributes before the native code
can page them, so cost grows with all drafts on the device; performance and
large-device behavior remain unverified. The warning intentionally conveys
only that an unclassified item exists on this device; its ownership is unknown.

These are synthetic backend tests and an iPhoneOS Swift object compile, not a
test of iOS Keychain enumeration, App/UTS native linkage, reinstall, device
locking, true process restart or the actual iPhone. The App page is not wired
to discovery and has not implemented paging, a warning, or restart recovery;
Android and Weixin continue to return `unsupported_platform`. None of this
implements server synchronization or changes the existing old-draft migration
and single-process limitations.
