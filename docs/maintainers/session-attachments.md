# Session attachment contract

Transcript image records are projected as timeline items with `kind: "attachment"`, `title: "Image"`, and text fallback `Image attachment`. The `attachment` object contains an opaque immutable ID, validated raster `mediaType`, and optional dimensions and byte count. Journal and SSE payloads carry this metadata and `callId`; they never carry image bytes or base64.

For Claude `tool_result` content, textual blocks remain in the `toolResult` item, while each image becomes a separate `attachment` item with the same `callId` equal to `tool_use_id`. The exact metadata shape is `{id, mediaType, width?, height?, bytes?}`; image bytes are fetched only through the attachment route.

The browser route is `GET /api/session-attachment?sessionId=<id>&id=<attachment-id>` and the bearer-protected API route is `/api/v1/session-attachment` with the same query. The route serves only an exact absolute local path or inline base64 value referenced by the addressed transcript record. It opens regular non-symlink files, validates PNG/JPEG/GIF bytes, enforces a 10 MiB decoded limit, verifies the content hash embedded in the attachment ID, and rejects URLs, SVG, malformed data, changed files, and unreferenced paths. Record lookup seeks directly to the offset encoded in the ID and reads one bounded JSONL record; it does not scan the retained journal or transcript tail.

Local-file delivery reads in 64 KiB chunks and checks request cancellation between chunks. This bounds retained read work and preserves cancellation errors for the API, but it does not promise a hard deadline if the operating-system file read itself is stalled.
