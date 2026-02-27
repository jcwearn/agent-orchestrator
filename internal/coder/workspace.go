package coder

import (
	"errors"
	"sync"
)

var (
	ErrNoFreeWorkspace  = errors.New("no free workspace available")
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceBusy     = errors.New("workspace is busy")
	ErrWorkspaceNotBusy  = errors.New("workspace is not busy")
)

// DefaultWorkspaces is the default set of Coder workspace names.
var DefaultWorkspaces = []string{"agent-1", "agent-2", "agent-3", "agent-4"}

// slot tracks whether a workspace is assigned to a task.
type slot struct {
	TaskID string // empty means free
}

// WorkspaceSlot is the public view of a pool slot, returned by Status().
type WorkspaceSlot struct {
	Name   string
	TaskID string
}

// Pool manages workspace-to-task assignments. It is purely slot management;
// starting/stopping workspaces is handled separately by the executor.
type Pool struct {
	mu    sync.Mutex
	slots map[string]*slot
}

// NewPool creates a Pool with the given workspace names.
func NewPool(workspaces []string) *Pool {
	slots := make(map[string]*slot, len(workspaces))
	for _, name := range workspaces {
		slots[name] = &slot{}
	}
	return &Pool{slots: slots}
}

// Acquire assigns the first free workspace to the given task and returns its name.
func (p *Pool) Acquire(taskID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for name, s := range p.slots {
		if s.TaskID == "" {
			s.TaskID = taskID
			return name, nil
		}
	}
	return "", ErrNoFreeWorkspace
}

// Release frees a workspace so it can be assigned to another task.
func (p *Pool) Release(workspace string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	s, ok := p.slots[workspace]
	if !ok {
		return ErrWorkspaceNotFound
	}
	if s.TaskID == "" {
		return ErrWorkspaceNotBusy
	}
	s.TaskID = ""
	return nil
}

// Status returns a snapshot of all workspace slots.
func (p *Pool) Status() []WorkspaceSlot {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]WorkspaceSlot, 0, len(p.slots))
	for name, s := range p.slots {
		result = append(result, WorkspaceSlot{Name: name, TaskID: s.TaskID})
	}
	return result
}

// FreeCount returns the number of unassigned workspaces.
func (p *Pool) FreeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for _, s := range p.slots {
		if s.TaskID == "" {
			count++
		}
	}
	return count
}
