# Session attachment contract

Session timeline readers expose image references without placing image bytes in
timeline rows, SQLite journal payloads, or SSE events. An image item has
`kind: "attachment"`, `title: "Image"`, fallback `text: "Image attachment"`,
an opaque stable `attachmentId`, and optional `mediaType`, `width`, `height`,
and `bytes`. Claude tool-result images retain their `callId`.

Claude base64 image blocks and Codex `input_image`, `local_image`, and user
image payloads are recognized only when the transcript references them. IDs
include the stable transcript item identity and a source-content fingerprint,
so a changed local file cannot silently be served under an immutable URL.

`GET /api/v1/session-attachment?sessionId=<session>&id=<attachmentId>` and the
cookie-guarded dashboard route use the existing read authorization. The
response is private and immutable, capped at 10 MiB, and validated from actual
PNG, JPEG, or GIF bytes. SVG, malformed data, unsupported formats, unknown
references, out-of-scope paths, and network URLs are not fetchable. Oversized
data returns `413 attachment_too_large`; malformed images return
`415 attachment_invalid`; missing or unreferenced records return
`404 attachment_not_found`.

Transcript lookup is bounded to a recent 16 MiB record window, large enough for
a 10 MiB image plus base64 and JSON overhead but never an unbounded history
scan. An older attachment outside that window is unavailable rather than
silently inferred or fetched from an arbitrary path.
