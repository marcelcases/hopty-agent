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

The immutable service installer injects a release version, SHA-256 digest, and service origin before it is served as `/install.sh`. It installs under `~/.hopty/`, verifies the binary, starts a user service when available, then runs `hopty pair`. It never invokes `sudo`; it only prints the optional `loginctl enable-linger` command.
