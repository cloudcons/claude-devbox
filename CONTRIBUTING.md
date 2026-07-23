# Contributing

Issues and pull requests are welcome.

## Before you open a PR

- **Go** (`claude-remote/`): `gofmt -w .`, `go vet ./...`, and make sure
  `go build ./...` is clean. The tool is deliberately a single small file
  with no dependencies outside the standard library — please keep it that
  way.
- **Ansible** (`ansible/`): `ansible-lint roles/devbox/` must pass, and
  `ansible-playbook -i inventory.example.yml devbox.yml --syntax-check`
  must succeed. The role targets Debian/Ubuntu with systemd; changes that
  add provider- or site-specific assumptions (a particular hypervisor,
  network, or secrets manager) are out of scope — that's what host_vars
  are for.

CI runs exactly these checks on every push and PR.

## Scope

The kit does one thing: take a stock Debian/Ubuntu box to "per-project
supervised Claude Code Remote Control sessions". Features that serve that
goal are welcome; general-purpose dotfile/workstation management is better
served by other tools.
