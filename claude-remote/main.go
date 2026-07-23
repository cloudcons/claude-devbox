// claude-remote provisions supervised Claude Code Remote Control servers as
// systemd user services — one per project. Each runs `claude remote-control`
// (server mode) in a dedicated tmux socket, with Restart=on-failure so it stays
// reachable from claude.ai/code for days.
//
// Usage:
//
//	claude-remote install                 # install the systemd template unit
//	claude-remote add <name> --clone owner/repo [--into dir]
//	claude-remote add <name> --workdir /path/to/project
//	claude-remote list                    # projects + service state
//	claude-remote attach <name>           # tmux-attach to see the pairing URL
//	claude-remote remove <name>           # stop + delete service (keeps the clone)
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed claude-remote@.service
var unitTemplate []byte

const unitName = "claude-remote@.service"

func home() string    { h, _ := os.UserHomeDir(); return h }
func unitDir() string { return filepath.Join(home(), ".config", "systemd", "user") }
func envDir() string  { return filepath.Join(home(), ".config", "claude-remote") }

func die(f string, a ...any)  { fmt.Fprintf(os.Stderr, "claude-remote: "+f+"\n", a...); os.Exit(1) }
func info(f string, a ...any) { fmt.Printf(f+"\n", a...) }

func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// systemctl --user helpers
func sc(a ...string) error { return run("systemctl", append([]string{"--user"}, a...)...) }
func scOut(a ...string) string {
	b, _ := exec.Command("systemctl", append([]string{"--user"}, a...)...).CombinedOutput()
	return strings.TrimSpace(string(b))
}

// tmux on a project's dedicated socket
func tmuxOut(name string, a ...string) string {
	b, _ := exec.Command("tmux", append([]string{"-L", "cc-" + name}, a...)...).CombinedOutput()
	return string(b)
}
func pane(name string) string { return tmuxOut(name, "capture-pane", "-p", "-t", name) }
func send(name string, keys string) {
	exec.Command("tmux", "-L", "cc-"+name, "send-keys", "-t", name, keys).Run()
}

func expand(p string) string {
	if p == "~" {
		return home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

func ensureInstalled() {
	must(os.MkdirAll(unitDir(), 0o755))
	must(os.MkdirAll(envDir(), 0o755))
	dst := filepath.Join(unitDir(), unitName)
	if cur, _ := os.ReadFile(dst); string(cur) != string(unitTemplate) {
		must(os.WriteFile(dst, unitTemplate, 0o644))
		sc("daemon-reload")
		info("installed %s", dst)
	}
}

// preseedTrust marks a project dir trusted in ~/.claude.json so the server does
// not stall on the "trust this folder?" dialog.
func preseedTrust(workdir string) {
	p := filepath.Join(home(), ".claude.json")
	b, err := os.ReadFile(p)
	if err != nil {
		info("  (trust preseed skipped: %v)", err)
		return
	}
	var d map[string]any
	if json.Unmarshal(b, &d) != nil {
		info("  (trust preseed skipped: ~/.claude.json not parseable)")
		return
	}
	proj, _ := d["projects"].(map[string]any)
	if proj == nil {
		proj = map[string]any{}
		d["projects"] = proj
	}
	e, _ := proj[workdir].(map[string]any)
	if e == nil {
		e = map[string]any{}
		proj[workdir] = e
	}
	e["hasTrustDialogAccepted"] = true
	nb, _ := json.MarshalIndent(d, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(p), ".claude.*.tmp")
	if err != nil {
		info("  (trust preseed skipped: %v)", err)
		return
	}
	tmp.Write(nb)
	tmp.Close()
	must(os.Rename(tmp.Name(), p))
	info("  trust pre-seeded for %s", workdir)
}

// driveConsent watches the server's tmux pane and answers the one-time prompts:
// "Enable Remote Control? (y/n)" and, for git repos, the spawn-mode choice.
func driveConsent(name string) bool {
	consent, spawn := false, false
	for i := 0; i < 45; i++ {
		p := pane(name)
		if strings.Contains(p, "Ready") || strings.Contains(p, "Connected") {
			return true
		}
		if !consent && strings.Contains(p, "Enable Remote Control?") {
			send(name, "y")
			time.Sleep(600 * time.Millisecond)
			send(name, "Enter")
			consent = true
			time.Sleep(2 * time.Second)
			continue
		}
		if !spawn && strings.Contains(p, "Choose [1/2]") {
			send(name, "Enter") // default: same-dir
			spawn = true
			time.Sleep(2 * time.Second)
			continue
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

func readWorkdir(envFile string) string {
	b, _ := os.ReadFile(envFile)
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "CLAUDE_WORKDIR="); ok {
			return v
		}
	}
	return "?"
}

func cmdAdd(argv []string) {
	var name, workdir, clone, into string
	var rest []string
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "--workdir":
			i++
			workdir = argv[i]
		case "--clone":
			i++
			clone = argv[i]
		case "--into":
			i++
			into = argv[i]
		default:
			if strings.HasPrefix(a, "--") {
				die("unknown flag %s", a)
			}
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 {
		die("usage: claude-remote add <name> [--clone owner/repo] [--workdir dir] [--into dir]")
	}
	name = rest[0]
	ensureInstalled()

	if clone != "" {
		dest := expand(into)
		if dest == "" {
			dest = filepath.Join(home(), "Work", clone) // ~/Work/<owner>/<repo>
		}
		if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
			must(os.MkdirAll(filepath.Dir(dest), 0o755))
			info("cloning %s -> %s", clone, dest)
			if err := run("gh", "repo", "clone", clone, dest); err != nil {
				die("clone failed: %v", err)
			}
		} else {
			info("already cloned: %s", dest)
		}
		workdir = dest
	}
	if workdir == "" {
		die("need --workdir or --clone")
	}
	workdir, _ = filepath.Abs(expand(workdir))
	if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
		die("workdir is not a directory: %s", workdir)
	}

	envFile := filepath.Join(envDir(), name+".env")
	content := fmt.Sprintf("# Consumed by claude-remote@%s.service\nCLAUDE_WORKDIR=%s\n", name, workdir)
	must(os.WriteFile(envFile, []byte(content), 0o600))
	info("wrote %s", envFile)
	preseedTrust(workdir)

	info("enabling claude-remote@%s ...", name)
	if err := sc("enable", "--now", "claude-remote@"+name+".service"); err != nil {
		die("enable failed: %v", err)
	}
	time.Sleep(4 * time.Second)
	info("answering one-time consent ...")
	if driveConsent(name) {
		info("\n✔ %s is online — look for it at claude.ai/code", name)
	} else {
		info("\n! consent not auto-confirmed. Run: claude-remote attach %s  (answer the prompt; it then stays up)", name)
	}
}

func cmdList() {
	ents, err := os.ReadDir(envDir())
	if err != nil {
		die("no projects (envDir missing) — run `claude-remote install` and `add` one")
	}
	fmt.Printf("  %-14s %-8s %-9s %s\n", "NAME", "ACTIVE", "ENABLED", "WORKDIR")
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".env")
		fmt.Printf("  %-14s %-8s %-9s %s\n", name,
			scOut("is-active", "claude-remote@"+name+".service"),
			scOut("is-enabled", "claude-remote@"+name+".service"),
			readWorkdir(filepath.Join(envDir(), e.Name())))
	}
}

func cmdRemove(name string) {
	sc("disable", "--now", "claude-remote@"+name+".service")
	os.Remove(filepath.Join(envDir(), name+".env"))
	info("removed service + env for %q (the git clone was left in place)", name)
}

func cmdAttach(name string) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		die("tmux not found")
	}
	// replace this process with the interactive attach
	must(syscall.Exec(bin, []string{"tmux", "-L", "cc-" + name, "attach", "-t", name}, os.Environ()))
}

func usage() {
	fmt.Print(`claude-remote — supervised Claude Code Remote Control servers (one per project)

  install                                    install/refresh the systemd user template
  add <name> --clone owner/repo [--into dir] clone, provision, start, auto-consent
  add <name> --workdir /path/to/project      provision an already-present project
  list                                       projects + service state
  attach <name>                              tmux-attach (see pairing URL / QR)
  remove <name>                              stop + delete the service (keeps the clone)
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "install":
		ensureInstalled()
		info("ready. Reminder: `loginctl enable-linger %s` keeps servers up across logout/boot.", os.Getenv("USER"))
	case "add":
		cmdAdd(os.Args[2:])
	case "list":
		cmdList()
	case "attach":
		if len(os.Args) < 3 {
			die("usage: claude-remote attach <name>")
		}
		cmdAttach(os.Args[2])
	case "remove":
		if len(os.Args) < 3 {
			die("usage: claude-remote remove <name>")
		}
		cmdRemove(os.Args[2])
	case "-h", "--help", "help":
		usage()
	default:
		die("unknown command %q (try --help)", os.Args[1])
	}
}

func must(err error) {
	if err != nil {
		die("%v", err)
	}
}
