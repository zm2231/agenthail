# Session attachment contract

Transcript image records are projected as timeline items with `kind: "attachment"`, `title: "Image"`, and text fallback `Image attachment`. The `attachment` object contains an opaque immutable ID, validated raster `mediaType`, and optional dimensions and byte count. Journal and SSE payloads carry this metadata and `callId`; they never carry image bytes or base64.

For Claude `tool_result` content, textual blocks remain in the `toolResult` item, while each image becomes a separate `attachment` item with the same `callId` equal to `tool_use_id`. The exact metadata shape is `{id, mediaType, width?, height?, bytes?}`; image bytes are fetched only through the attachment route.

The browser route is `GET /api/session-attachment?sessionId=<id>&id=<attachment-id>` and the bearer-protected API route is `/api/v1/session-attachment` with the same query. The route serves only an exact absolute local path or inline base64 value referenced by the addressed transcript record. It opens regular non-symlink files, validates PNG/JPEG/GIF bytes, enforces a 10 MiB decoded limit, verifies the content hash embedded in the attachment ID, and rejects URLs, SVG, malformed data, changed files, and unreferenced paths. Record lookup seeks directly to the offset encoded in the ID and reads one bounded JSONL record; it does not scan the retained journal or transcript tail.

Local-file delivery reads in 64 KiB chunks and checks request cancellation between chunks. This bounds retained read work and preserves cancellation errors for the API, but it does not promise a hard deadline if the operating-system file read itself is stalled.

## Sending files

A person's attached files reach a session in two steps, both routed to the daemon that owns the session (see [hosts](hosts.md#routing)):

1. `POST /api/v1/uploads?sessionId=<id>&name=<file name>` with the raw bytes as the body (`control` scope). The daemon stores the file content-addressed at `~/.agenthail/uploads/<sha256>/<name>` and answers `{"upload": {id, name, mediaType, bytes, width?, height?}}`. The answer never carries the stored path. A PNG, JPEG or GIF with a decodable header is an image and is limited to 5 MiB, the largest image every runtime accepts; any other file is limited to 25 MiB. HEIC is not decoded on the daemon, so clients convert photos to JPEG first. Uploading the same bytes under the same name returns the same id. Uploads older than 7 days are removed hourly.
2. `POST /api/v1/actions` with `{"action": "send", ..., "attachments": ["upload:..."]}` (at most 8). The message text may be empty when attachments are present. An id this host does not hold is refused with `400`; attachments are refused on every other action and on sessions whose capabilities do not include `attach`.

The daemon resolves the ids to stored files and gives them to the runtime in the form it accepts:

- Codex, on every transport, receives each image as a `localImage` turn input.
- Claude receives each image as a base64 `image` content block when the person's turn goes through its Remote Control bridge.
- Every other file, and every image on a route without image input (the Claude peer socket), is named in the message as `Attached file: <stored path>`. The path is on the owning daemon's machine, which is where the agent runs, so clients never compose paths themselves.

A busy session never steers a message with attachments into the running turn; it waits in the queue with its attachments for a turn of its own.

A sent image renders from the runtime's own transcript record: Codex records the image input as an `input_image`, Claude as an `image` block, and both project to the `attachment` items above. The text Codex wraps around each image input (`<image name=[Image #1] path=...>`, `</image>`) and the meta record Claude writes naming where it saved a turn's images (`[Image: source: ...]`) are dropped from the timeline, as is the empty text of a tool result that holds only images.
