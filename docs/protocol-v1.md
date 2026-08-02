# Hopty protocol v1

## Status

Protocol version 1 is the public protocol between Hopty agents, browsers, and the hosted control plane. It is intentionally small. It carries control and WebRTC signaling only; terminal content never transits, persists in, or is logged by the control plane.

A peer that does not support version `1` must fail closed with `unsupported_version` and an actionable upgrade message.

## Encoding and limits

Control and signaling use UTF-8 JSON objects. Every object has exactly these envelope fields before its payload:

```json
{
  "version": 1,
  "type": "example",
  "request_id": "base64url-random-id",
  "payload": {}
}
```

- `version` is the JSON number `1`.
- `type` is one of the message types in this document.
- `request_id` is 16 random bytes encoded as unpadded base64url. It correlates a request with its success or failure response. Unsolicited events use a fresh request ID.
- `payload` is a JSON object with only the documented fields.
- Unknown envelope or payload fields, duplicate JSON keys, malformed UTF-8, and non-finite numbers are rejected.
- A control or signaling JSON message is at most 64 KiB.
- A WebRTC SDP string is at most 32 KiB.
- An ICE candidate string is at most 4 KiB. At most 64 candidates per peer and terminal are accepted.
- IDs are opaque, unpadded base64url strings encoding 16 random bytes unless a field says otherwise.
- Timestamps are RFC 3339 UTC strings.

The control plane accepts no message type that includes terminal input, terminal output, shell commands, executable paths, environment variables, or working directories.

## Error messages

Errors use this envelope:

```json
{
  "version": 1,
  "type": "error",
  "request_id": "request-id-being-rejected",
  "payload": {
    "code": "invalid_message",
    "retryable": false
  }
}
```

Stable error codes are:

| Code | Retryable | Meaning |
|---|---:|---|
| `unsupported_version` | no | Upgrade the incompatible peer. |
| `invalid_message` | no | JSON schema, size, or state validation failed. |
| `unauthorized` | no | Authentication or ownership validation failed. |
| `not_found` | no | The referenced opaque resource is unavailable to this caller. |
| `conflict` | no | The requested state transition was already consumed or superseded. |
| `expired` | no | The request or credential material expired. |
| `agent_offline` | yes | The linked agent is not connected to the control plane. |
| `capacity` | yes | A bounded local or service resource limit was reached. |
| `temporarily_unavailable` | yes | Retry after a bounded delay. |
| `internal` | yes | An internal failure occurred; no internal detail is exposed. |

Frontend copy maps these codes to concise human text. Protocol messages contain no diagnostic detail or secret values.

## Agent control authentication

The agent owns an Ed25519 identity key generated locally. Its public key identifies an agent; the private key never leaves the agent.

1. The agent connects to `wss://<origin>/api/v1/agent/control`.
2. It sends `agent.hello`:

```json
{
  "version": 1,
  "type": "agent.hello",
  "request_id": "...",
  "payload": {
    "agent_public_key": "base64url-ed25519-public-key",
    "agent_version": "semantic-version",
    "protocol_versions": [1]
  }
}
```

3. The service replies `agent.challenge` with a 32-byte random nonce:

```json
{
  "version": 1,
  "type": "agent.challenge",
  "request_id": "...",
  "payload": {
    "nonce": "base64url-32-byte-value"
  }
}
```

4. The agent signs the ASCII bytes `hopty-agent-auth-v1\n` followed by the raw nonce and returns `agent.prove`:

```json
{
  "version": 1,
  "type": "agent.prove",
  "request_id": "...",
  "payload": {
    "signature": "base64url-ed25519-signature"
  }
}
```

5. The service replies `agent.ready`:

```json
{
  "version": 1,
  "type": "agent.ready",
  "request_id": "...",
  "payload": {
    "agent_id": "opaque-agent-id",
    "paired": false
  }
}
```

The nonce is single-use and expires after 30 seconds. The service permits one live authenticated control connection per agent; a newly authenticated connection replaces a prior one.

## Pairing

Only an authenticated agent may create, cancel, or revoke pairing material.

### Create

The agent sends:

```json
{
  "version": 1,
  "type": "pairing.create",
  "request_id": "...",
  "payload": {
    "username": "marcel",
    "hostname": "vps.example"
  }
}
```

`username` and `hostname` are obtained locally by the agent and sent only after its control connection has authenticated. The service uses them to label a newly registered passkey as `username@hostname`; pairing-page input never controls that label. Older agents may send an empty payload and retain the `Hopty` fallback label.

The service cancels any pending request for that agent, creates one request, and returns raw material exactly once:

```json
{
  "version": 1,
  "type": "pairing.created",
  "request_id": "...",
  "payload": {
    "pairing_url": "https://hopty.net/pair#base64url-32-byte-token",
    "verification_code": "X97C",
    "expires_at": "2026-01-01T00:00:00Z"
  }
}
```

The URL token is 32 cryptographically random bytes encoded as unpadded base64url and is carried only in the URL fragment, so browsers never send it in an HTTP request path or referrer. The browser posts the token and code to the API verification endpoint in a bounded JSON body. The verification code is exactly four case-insensitive Crockford Base32 characters from `0123456789ABCDEFGHJKMNPQRSTVWXYZ`. No separators or whitespace are valid. The pair expires exactly two minutes after creation; the service rejects stale tokens with `410 Gone` during API verification.

The service stores only a SHA-256 token digest and a request-scoped HMAC-SHA-256 code digest.

### Cancel and state event

The agent sends `pairing.cancel` with an empty payload. The service replies `pairing.cancelled`; cancellation is idempotent.

The service sends an unsolicited `pairing.state` after each state transition:

```json
{
  "version": 1,
  "type": "pairing.state",
  "request_id": "...",
  "payload": {
    "state": "pending|consumed|failed|expired|cancelled|completed"
  }
}
```

No browser metadata, URL token, code, credential, or WebAuthn payload appears in this event.

### Browser verification state machine

```text
pending --correct code before expiry--> consumed --> registration_pending --> completed
pending --wrong/malformed/concurrent attempt--> failed
pending --2 minutes elapsed--> expired
pending --agent cancellation/new request--> cancelled
consumed --registration cancelled/invalid/expired--> failed
```

All terminal states are permanent. Refreshing, reopening, replaying, or concurrent use of a terminal-state link never changes it. Exactly one browser verification submission claims a pending request. A correct submission creates one registration ticket and one WebAuthn registration challenge, each valid for two minutes, before replying to the browser.

## Agent status and credential revocation

The agent may request its current status with `agent.status` and an empty payload. The service replies with `agent.status`:

```json
{
  "version": 1,
  "type": "agent.status",
  "request_id": "...",
  "payload": {
    "agent_version": "v0.1.0-beta.1",
    "paired": true,
    "passkey_created_at": "2026-07-10T10:30:14Z",
    "last_access_at": "2026-07-08T19:08:22Z",
    "sessions": [{
      "user": "marcel@vps",
      "connection": "direct|relayed",
      "transport": "WebRTC|TURN",
      "latency_ms": 5,
      "incoming_ip": "203.0.113.7"
    }]
  }
}
```

`last_access_at` and the timestamp fields may be omitted when no value exists. Active sessions contain connection metadata only; terminal bytes never appear in status messages.

The agent sends `credential.revoke` with an empty payload. The service disables the credential, revokes every browser session and terminal lease for the agent, then sends the agent one `terminal.close` event per active terminal:

```json
{
  "version": 1,
  "type": "terminal.close",
  "request_id": "...",
  "payload": {
    "terminal_id": "opaque-terminal-id",
    "reason": "credential_revoked|credential_replaced"
  }
}
```

Successful passkey replacement follows the same durable revocation rule before the new browser session is issued. An agent must terminate any terminal listed in `terminal.close`; after reconnect it must also terminate every locally active terminal not present in the service's accepted resynchronization response.

## Browser terminal lifecycle and signaling

The browser's authenticated control WebSocket uses the same envelope and limits. The browser must have an active browser session and own the terminal ID.

### Create a terminal

The browser requests a terminal through HTTPS. The service creates a terminal lease and sends the agent:

```json
{
  "version": 1,
  "type": "terminal.open",
  "request_id": "...",
  "payload": {
    "terminal_id": "opaque-terminal-id",
    "ice_servers": [
      {
        "urls": ["stun:example:3478", "turn:example:3478?transport=udp"],
        "username": "short-lived-turn-username",
        "credential": "short-lived-turn-credential"
      }
    ]
  }
}
```

The agent replies `terminal.accepted` or `terminal.rejected`:

```json
{
  "version": 1,
  "type": "terminal.accepted",
  "request_id": "...",
  "payload": { "terminal_id": "opaque-terminal-id" }
}
```

```json
{
  "version": 1,
  "type": "terminal.rejected",
  "request_id": "...",
  "payload": {
    "terminal_id": "opaque-terminal-id",
    "code": "capacity|temporarily_unavailable"
  }
}
```

### Signaling

The browser creates the offer and the agent creates the answer. All signaling payloads include the authorized `terminal_id`:

```json
{
  "version": 1,
  "type": "webrtc.offer|webrtc.answer",
  "request_id": "...",
  "payload": {
    "terminal_id": "opaque-terminal-id",
    "sdp": "SDP string"
  }
}
```

```json
{
  "version": 1,
  "type": "webrtc.candidate",
  "request_id": "...",
  "payload": {
    "terminal_id": "opaque-terminal-id",
    "candidate": "candidate string",
    "sdp_mid": "0",
    "sdp_mline_index": 0
  }
}
```

```json
{
  "version": 1,
  "type": "webrtc.ice_restart",
  "request_id": "...",
  "payload": { "terminal_id": "opaque-terminal-id" }
}
```

A candidate may use `null` `candidate`, `sdp_mid`, and `sdp_mline_index` only to signal end-of-candidates. The service validates ownership, message sequence, lengths, and candidate count but does not persist, log, alter, or proxy SDP/candidates beyond forwarding the in-memory message.

### Connection status

After the DataChannel opens, the browser sends a bounded signaling status message every few seconds:

```json
{"type":"status","data":{"connection":"direct|relayed","transport":"WebRTC|TURN","latency_ms":5}}
```

The service stores only these control-plane fields for `hopty status`; it never stores or forwards terminal DataChannel bytes.

### Closure and resynchronization

The agent reports terminal closure:

```json
{
  "version": 1,
  "type": "terminal.closed",
  "request_id": "...",
  "payload": {
    "terminal_id": "opaque-terminal-id",
    "reason": "shell_exit|channel_closed|recovery_timeout|agent_shutdown|credential_revoked|credential_replaced",
    "exit_status": 0
  }
}
```

`exit_status` is omitted unless `reason` is `shell_exit`. Closure is idempotent. When the final terminal lease of a browser session closes, the service revokes the browser session.

After each control-connection authentication/reconnection, the agent sends:

```json
{
  "version": 1,
  "type": "terminal.sync",
  "request_id": "...",
  "payload": {
    "active_terminal_ids": ["opaque-terminal-id"]
  }
}
```

The service returns:

```json
{
  "version": 1,
  "type": "terminal.sync_result",
  "request_id": "...",
  "payload": {
    "accepted_terminal_ids": ["opaque-terminal-id"],
    "terminate_terminal_ids": ["opaque-terminal-id"]
  }
}
```

## Direct terminal DataChannel

The browser and agent negotiate one reliable, ordered WebRTC DataChannel named `hopty.terminal.v1`. Its frames are binary and never sent to the control plane.

| Byte 0 | Direction | Remaining bytes | Validation |
|---|---|---|---|
| `0x01` | both | PTY bytes | Maximum 64 KiB frame. Browser input is sent only to the PTY; agent output is sent only to the renderer. |
| `0x02` | browser → agent | 2-byte unsigned big-endian columns, then 2-byte unsigned big-endian rows | Exactly 4 bytes; both values are 1–1000. |
| `0x03` | agent → browser | 4-byte signed big-endian exit status, then 1-byte close reason | Exactly 5 bytes; sent once before normal shell-exit closure when channel state permits. |
| `0x04` | both | UTF-8 protocol error | At most 256 bytes, valid UTF-8, and never terminal content. |

Unknown frame types, invalid lengths, invalid dimensions, oversized frames, invalid UTF-8 errors, and invalid direction close the terminal with `invalid_message`.

Close-reason enum values are `0x01 shell_exit`, `0x02 channel_closed`, `0x03 recovery_timeout`, `0x04 agent_shutdown`, `0x05 credential_revoked`, and `0x06 credential_replaced`.

The agent starts the current Unix user's interactive login shell in a fresh PTY. It does not accept shell commands, paths, environment variables, or working directories from the browser or service.

A DataChannel transition to disconnected starts a single 10-second ICE-recovery deadline. A recovered connection cancels the deadline. A failed or closed channel terminates the PTY immediately; an uncleared deadline terminates it when it expires. The agent sends SIGHUP, then SIGKILL after a short bounded wait if needed, and reaps the shell.

## Compatibility

Within v1, optional fields may be added only when receivers can ignore them safely. Existing field meanings, required fields, state transitions, message types, limits, and binary frame layouts are immutable. Any incompatible change requires protocol v2 and a separate document.
