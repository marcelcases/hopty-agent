# Hopty agent

The public Linux agent for Hopty browser terminals.

- Go single binary for Linux amd64 and arm64.
- Runs as the local Unix user; never requires permanent privilege.
- Public protocol: [`docs/protocol-v1.md`](docs/protocol-v1.md).

## Development

Only Docker and Docker Compose are required on the host.

```sh
make test
make build
make dev
```

## Installation

The immutable service installer injects a release version, SHA-256 digest, and service origin before it is served as `/install.sh`. It installs under `~/.hopty/`, verifies the binary, starts a user service when available, then runs `hopty pair`. It never invokes `sudo`; it only prints the optional `loginctl enable-linger` command. During pairing, the agent supplies its local Unix username and hostname on its authenticated control connection so the newly created passkey is labelled `user@hostname`.

The service Pages build resolves the latest published release from GitHub metadata, so agent version/checksum values are not Pages settings. The release workflow optionally refreshes the staging Pages build when the `HOPTY_PAGES_STAGING_DEPLOY_HOOK` GitHub secret is configured; it is disabled by default.

Direct WebRTC uses agent UDP ports `55000-55099`. Hosts with a default-deny firewall must allow that inbound range at both host and provider firewalls to avoid TURN relay fallback. Terminal traffic remains DTLS-encrypted. No firewall change is needed when relayed operation is acceptable.
