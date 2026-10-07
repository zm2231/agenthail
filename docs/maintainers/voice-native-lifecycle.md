# Native voice-call heartbeat lifecycle

The native host keeps an accepted call alive with an explicit action heartbeat.
This is independent of transcript polling and timeline reads.

## Contract

POST `/api/v1/voice` with the paired device bearer token and:

```json
{"action":"heartbeat","attemptId":"<current-attempt-id>"}
```

The host validates the paired owner and the current active `attemptId`. A
foreign owner or stale attempt is rejected and does not renew liveness. A
valid heartbeat only updates the host liveness timestamp and durable native
heartbeat enrollment; it does not call the Codex provider or read the event
stream. The native main model sends the first heartbeat after the host reports
the call start/connection acknowledgment, then sends one every 10 seconds. It
stops sending when local hangup begins.

Once native heartbeat enrollment has occurred, `GET /api/v1/voice` does not
renew liveness. This prevents an idle UI, transcript reader, or timeline poll
from keeping a native call alive. The existing browser consumer may continue
using `GET /api/voice` as its liveness signal when it has not enrolled native
heartbeat.

The service expires an unrenewed call after about 40 seconds by requesting the
native audio stop. The terminal reason `phone_disconnected` is retained through
the native close event. An explicit local stop records `local_hangup` when no
more specific terminal reason is already present. No audio, SDP, or provider
payload is written as heartbeat evidence.

## Restart boundary

An active call retained in `operator.json` is changed to `unknown` when the
host starts again. The host does not claim that media remains connected and
does not reattach or restart media from persisted state. The user must end the
uncertain call and establish a new one.

The heartbeat is an action on the existing voice API, so it uses the same
paired-device authorization, owner lease, and action response envelope as the
other voice actions. The response is the current lightweight voice state; no
provider round trip is required.
