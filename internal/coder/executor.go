package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
)

// CommandCreator is an injectable factory for creating exec.Cmd instances.
// Defaults to exec.CommandContext; tests inject a fake for deterministic behavior.
type CommandCreator func(ctx context.Context, name string, args ...string) *exec.Cmd

// SSHResult holds the outcome of a command executed over SSH.
// A non-nil SSHResult means the command ran (even if it exited non-zero);
// a nil SSHResult with a non-nil error means the command could not start.
type SSHResult struct {
	ExitCode int
}

// WorkspaceExecutor is consumed by the orchestrator (Phase 3) and enables
// mock-based testing without a real Coder CLI.
type WorkspaceExecutor interface {
	SSH(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*SSHResult, error)
	StartWorkspace(ctx context.Context, workspace string, params map[string]string) error
	StopWorkspace(ctx context.Context, workspace string) error
	ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error)
}

// Executor wraps the Coder CLI to manage workspaces and run commands.
type Executor struct {
	newCmd CommandCreator
	logger *slog.Logger
}

// NewExecutor creates an Executor. If newCmd is nil it defaults to exec.CommandContext.
func NewExecutor(logger *slog.Logger, newCmd CommandCreator) *Executor {
	if newCmd == nil {
		newCmd = exec.CommandContext
	}
	return &Executor{newCmd: newCmd, logger: logger}
}

// SSH runs a command inside a Coder workspace via `coder ssh`.
// stdout and stderr are streamed to the provided writers in real-time.
func (e *Executor) SSH(ctx context.Context, workspace, command string, stdout, stderr io.Writer) (*SSHResult, error) {
	cmd := e.newCmd(ctx, "coder", "ssh", workspace, "--", "bash", "-c", command)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	e.logger.Debug("executing ssh command", "workspace", workspace, "command", command)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &SSHResult{ExitCode: exitErr.ExitCode()}, fmt.Errorf("command exited with code %d: %w", exitErr.ExitCode(), err)
		}
		return nil, fmt.Errorf("ssh exec failed: %w", err)
	}
	return &SSHResult{ExitCode: 0}, nil
}

// StartWorkspace starts a Coder workspace via `coder start --yes`.
// Template parameters (e.g. repo_url) are passed as --parameter key=value flags.
func (e *Executor) StartWorkspace(ctx context.Context, workspace string, params map[string]string) error {
	args := []string{"start", workspace, "--yes"}
	for k, v := range params {
		args = append(args, "--parameter", k+"="+v)
	}
	cmd := e.newCmd(ctx, "coder", args...)

	e.logger.Debug("starting workspace", "workspace", workspace, "params", params)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start workspace %q: %s: %w", workspace, string(out), err)
	}
	return nil
}

// StopWorkspace stops a Coder workspace via `coder stop --yes`.
func (e *Executor) StopWorkspace(ctx context.Context, workspace string) error {
	cmd := e.newCmd(ctx, "coder", "stop", workspace, "--yes")

	e.logger.Debug("stopping workspace", "workspace", workspace)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stop workspace %q: %s: %w", workspace, string(out), err)
	}
	return nil
}

// ListWorkspaces returns info about all workspaces via `coder list --output json`.
func (e *Executor) ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	cmd := e.newCmd(ctx, "coder", "list", "--output", "json")

	e.logger.Debug("listing workspaces")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %s: %w", string(out), err)
	}

	var raw []coderWorkspace
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse workspace list: %w", err)
	}

	infos := make([]WorkspaceInfo, len(raw))
	for i, w := range raw {
		infos[i] = WorkspaceInfo{
			Name:   w.Name,
			Status: parseWorkspaceStatus(w.LatestBuild.Status),
		}
	}
	return infos, nil
}

// coderWorkspace mirrors the relevant fields from the Coder CLI JSON output.
type coderWorkspace struct {
	Name        string     `json:"name"`
	LatestBuild coderBuild `json:"latest_build"`
}

type coderBuild struct {
	Status string `json:"status"`
}

// WorkspaceStatus represents the state of a Coder workspace.
type WorkspaceStatus string

const (
	WorkspaceStatusRunning   WorkspaceStatus = "running"
	WorkspaceStatusStopped   WorkspaceStatus = "stopped"
	WorkspaceStatusFailed    WorkspaceStatus = "failed"
	WorkspaceStatusStarting  WorkspaceStatus = "starting"
	WorkspaceStatusStopping  WorkspaceStatus = "stopping"
	WorkspaceStatusPending   WorkspaceStatus = "pending"
	WorkspaceStatusCanceling WorkspaceStatus = "canceling"
	WorkspaceStatusCanceled  WorkspaceStatus = "canceled"
	WorkspaceStatusDeleting  WorkspaceStatus = "deleting"
	WorkspaceStatusDeleted   WorkspaceStatus = "deleted"
	WorkspaceStatusUnknown   WorkspaceStatus = "unknown"
)

// WorkspaceInfo holds the name and current status of a workspace.
type WorkspaceInfo struct {
	Name   string
	Status WorkspaceStatus
}

func parseWorkspaceStatus(s string) WorkspaceStatus {
	switch WorkspaceStatus(s) {
	case WorkspaceStatusRunning,
		WorkspaceStatusStopped,
		WorkspaceStatusFailed,
		WorkspaceStatusStarting,
		WorkspaceStatusStopping,
		WorkspaceStatusPending,
		WorkspaceStatusCanceling,
		WorkspaceStatusCanceled,
		WorkspaceStatusDeleting,
		WorkspaceStatusDeleted:
		return WorkspaceStatus(s)
	default:
		return WorkspaceStatusUnknown
	}
}
