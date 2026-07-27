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

The agent implementation is introduced incrementally; pairing and terminal commands are not available yet.
