package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// TmuxHasSession returns true if a tmux session with the given name exists.
func TmuxHasSession(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// TmuxNewSession creates a detached tmux session running the given shell.
// The session is created with a large default size so initial output isn't cramped.
// Mouse mode is enabled so wheel events (from xterm.js, including our touch->wheel
// dispatch) scroll into tmux copy-mode for scrollback.
func TmuxNewSession(name, shell string) error {
	if shell == "" {
		shell = "bash"
	}
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-x", "220", "-y", "50", shell)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Best-effort: enable mouse mode per-session. Ignore failures (older tmux
	// versions may use a different option name) — scrollback will be the only
	// thing that breaks, the session still works.
	_ = exec.Command("tmux", "set-option", "-t", name, "mouse", "on").Run()
	// Disable tmux's status bar. Tmux redraws the status line on every keystroke
	// and timer tick; those redraws get captured into aurex's ring buffer and
	// pollute ghostty's scrollback (the status bar appears stacked over and
	// over as you scroll back). With status off, tmux only emits actual pane
	// content, which flows naturally into linear scrollback.
	_ = exec.Command("tmux", "set-option", "-t", name, "status", "off").Run()
	// Let OSC notification sequences (9, 99, 777) from agents pass through
	// tmux to aurex's PTY capture, so we can detect "agent waiting" without a
	// shell hook. allow-passthrough is opt-in for safety reasons (escape
	// injection) — fine for aurex since we control the capture endpoint.
	_ = exec.Command("tmux", "set-option", "-t", name, "allow-passthrough", "on").Run()
	return nil
}

// TmuxConfigureSilenceHook wires "agent went idle" detection into tmux:
//
//  1. monitor-silence N — window option; fires the alert-silence hook after
//     N seconds of no output in the pane.
//  2. silence-action none — suppresses tmux's bell/banner; aurex handles the
//     user-facing notification itself.
//  3. alert-silence hook (server-global) — runs a curl that POSTs to aurex's
//     /api/hook/aura with the firing session's name substituted at hook time.
//
// monitor-silence is per-window; alert-silence is the hook tmux calls for
// any window's silence event, so we set it once globally and let the
// #{session_name} format pick the right session at fire time. Idempotent:
// set-hook -R replaces an existing hook of the same name.
func TmuxConfigureSilenceHook(name string, intervalSec, port int) {
	// Apply per-window settings to every window in this session (in practice
	// each aurex session has exactly one window).
	_ = exec.Command("tmux", "set-window-option", "-t", name, "monitor-silence", fmt.Sprintf("%d", intervalSec)).Run()
	_ = exec.Command("tmux", "set-window-option", "-t", name, "silence-action", "none").Run()

	hookCmd := fmt.Sprintf(
		`run-shell -b "curl -s -m 2 -X POST -d active=true -d 'reason=Agent idle' -d 'session=#{session_name}' http://127.0.0.1:%d/api/hook/aura"`,
		port,
	)
	// Global alert-silence so every session's silence event fires it.
	_ = exec.Command("tmux", "set-hook", "-g", "-R", "alert-silence", hookCmd).Run()
}

// TmuxKillSession terminates the named tmux session.
func TmuxKillSession(name string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// TmuxListSessions returns the names of all live tmux sessions.
func TmuxListSessions() ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// tmux returns non-zero when no server is running. That's not an error for us.
		if strings.Contains(string(out), "no server running") || strings.Contains(string(out), "error connecting") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	res := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, l)
		}
	}
	return res, nil
}

// TmuxPaneCWD returns the current working directory of the session's active pane.
func TmuxPaneCWD(name string) (string, error) {
	cmd := exec.Command("tmux", "display-message", "-p", "-t", name, "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
