# claude-devbox

Turn any Linux box into a **Claude Code dev box**: an unprivileged dev user
with a per-project toolchain, and one supervised **Claude Code Remote
Control** server per repo — so every project shows up as its own session at
[claude.ai/code](https://claude.ai/code) and in the Claude mobile app, stays
paired for days, and comes back by itself after crashes and reboots.

Works on anything Debian/Ubuntu that runs systemd: a cloud VM, a Proxmox/ESXi
VM, bare metal, or a container that boots systemd. Nothing here assumes a
particular hypervisor or network.

## Architecture

```
systemd (user manager, linger enabled)
 ├─ claude-remote@app.service        Restart=on-failure
 │   └─ tmux server (socket cc-app)
 │       └─ claude remote-control    rooted in ~/Work/app
 ├─ claude-remote@docs.service
 │   └─ tmux (cc-docs) → claude remote-control   rooted in ~/Work/docs
 └─ ...one unit per project
```

Key properties:

- **One server per project.** Each repo is its own session on claude.ai/code,
  with its own history and working directory. Adding/removing a project never
  touches the others (each tmux server lives on its own socket).
- **Supervised.** `Restart=on-failure` brings a server back after a crash,
  an upgrade, or the >10-minute-offline disconnect. `loginctl enable-linger`
  makes the whole tree start at boot, no login needed.
- **Dormant until authenticated.** The unit is gated on
  `~/.claude/.credentials.json` existing, so it sits idle (not crash-looping)
  until you run `claude auth login` once.

The moving parts:

| Piece | What it is |
|---|---|
| `claude-remote/` | ~300-line Go CLI (no runtime deps) that installs the systemd template and manages per-project instances: `install` / `add` / `list` / `attach` / `remove`. `add` also pre-seeds folder trust and auto-answers the one-time consent prompts, so provisioning is hands-off. |
| `claude-remote/claude-remote@.service` | The systemd **user** template unit (embedded in the binary; shown for reference). |
| `ansible/` | A `devbox` role + playbook that takes a fresh Debian/Ubuntu host to "ready for handoff": packages, Claude Code (apt), dev user, mise toolchain, repo clones, the claude-remote binary, linger. |

## Requirements

- Debian or Ubuntu with systemd (root or sudo access to provision).
- A **claude.ai account** (Pro/Max/Team/Enterprise). Remote Control rejects
  API keys and long-lived tokens by design — the one-time browser OAuth
  (`claude auth login`) is required, so plan one interactive minute per box.
- Claude Code ≥ 2.1.196 (the apt repo installed by the role satisfies this).

## Path A — Ansible (recommended)

```sh
cd ansible
cp inventory.example.yml inventory.yml            # point it at your host
cp host_vars/devbox1.yml.example host_vars/<host>.yml   # keys, tools, repos
ansible-playbook -i inventory.yml devbox.yml
```

Needs the `community.general` and `ansible.posix` collections
(`ansible-galaxy collection install community.general ansible.posix`).

The role is idempotent — re-run it any time. It ends by printing the box's
generated SSH public key (add it as a deploy key if you clone private repos
over SSH) and the handoff steps below.

## Path B — manual (10 minutes, no Ansible)

As root on the box:

```sh
apt-get install -y git curl ca-certificates gnupg tmux ripgrep jq sudo openssh-client
curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o /etc/apt/keyrings/claude-code.asc
echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/stable stable main" \
  > /etc/apt/sources.list.d/claude-code.list
apt-get update && apt-get install -y claude-code
useradd -m -s /bin/bash dev
loginctl enable-linger dev
```

As the dev user:

```sh
curl -fsSL https://mise.run | sh                       # toolchain manager
echo 'eval "$(~/.local/bin/mise activate bash)"' >> ~/.bashrc
cd /path/to/claude-devbox/claude-remote
mise install && mise run install                       # -> ~/.local/bin/claude-remote
```

## Handoff — the two interactive steps

SSH in as the dev user, then:

```sh
claude auth login                       # one-time browser OAuth
claude-remote add app  --workdir ~/Work/app
claude-remote add docs --workdir ~/Work/docs
# or clone+provision in one step (needs gh, authenticated):
claude-remote add api  --clone acme/api
```

Each `add` writes the per-project env file, pre-seeds folder trust, enables
`claude-remote@<name>.service`, and auto-answers the one-time "Enable Remote
Control?" and spawn-mode prompts. Within seconds the project appears at
claude.ai/code and in the mobile app. If auto-consent ever misses, `claude-remote
attach <name>` shows the live pane (and the pairing QR); answer once and it
stays up.

## Day-2 operations

```sh
claude-remote list                       # projects + active/enabled state
claude-remote attach app                 # watch the live session (Ctrl-b d to detach)
claude-remote remove app                 # stop + delete the service (keeps the clone)
systemctl --user status 'claude-remote@*'
journalctl --user -u claude-remote@app
```

- **New project**: clone it under `~/Work`, `claude-remote add <name> --workdir …`.
- **PR-review worktrees**: treat them like projects — `add` when you create
  the worktree, **`remove` before deleting the worktree** (otherwise the unit
  restarts into a missing directory).
- **Upgrading Claude Code**: running servers keep the old binary; after an
  apt upgrade, `systemctl --user restart 'claude-remote@*'` (sessions
  re-pair automatically).
- **Reboots**: everything returns on its own (linger + `WantedBy=default.target`).

## Security notes

- The dev user gets **passwordless sudo** by default (`devbox_sudo_nopasswd`)
  — right for a personal disposable box, wrong for shared machines. Turn it
  off and scope privileges yourself if others log in.
- Give the box **only the credentials its projects need** (deploy keys over
  account-wide PATs; per-org tokens over global ones). Anyone who can drive
  the Claude session can use whatever the box can reach — the session runs
  with the dev user's permissions, gated by your approval of its actions.
- `~/.claude/.credentials.json` is the box's claude.ai identity. Treat the
  box like a logged-in laptop: full-disk encryption and SSH-key-only login
  are worth it.
- A workspace `.claude/settings.json` permission policy (allow/deny lists
  for tools and commands) is the right place to encode what sessions may do
  unprompted — ship one per repo if you have opinions.

## Repo layout

```
claude-remote/            the Go tool (vendored, self-contained)
ansible/
  devbox.yml              playbook
  inventory.example.yml
  host_vars/devbox1.yml.example
  roles/devbox/           the provisioning role
```
