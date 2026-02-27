package orchestrator

import (
	"bytes"
	"context"
	"strings"

	"github.com/jcwearn/agent-orchestrator/internal/store"
)

// logWriter splits written bytes into lines and persists each line as a TaskLog.
// It also accumulates all output for later retrieval via String().
type logWriter struct {
	ctx    context.Context
	store  *store.Store
	taskID string
	step   string
	stream string
	buf    bytes.Buffer // partial line buffer
	full   bytes.Buffer // accumulated output
}

func (o *Orchestrator) newLogWriter(ctx context.Context, taskID, step, stream string) *logWriter {
	return &logWriter{
		ctx:    ctx,
		store:  o.store,
		taskID: taskID,
		step:   step,
		stream: stream,
	}
}

func (w *logWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.full.Write(p)
	w.buf.Write(p)

	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No newline found — put the partial line back in the buffer.
			w.buf.WriteString(line)
			break
		}
		line = strings.TrimRight(line, "\n")
		if err := w.store.CreateTaskLog(w.ctx, &store.TaskLog{
			TaskID: w.taskID,
			Step:   w.step,
			Stream: w.stream,
			Line:   line,
		}); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Flush writes any remaining partial line as a final log entry.
func (w *logWriter) Flush() error {
	remaining := w.buf.String()
	if remaining == "" {
		return nil
	}
	w.buf.Reset()
	return w.store.CreateTaskLog(w.ctx, &store.TaskLog{
		TaskID: w.taskID,
		Step:   w.step,
		Stream: w.stream,
		Line:   remaining,
	})
}

// String returns all accumulated output.
func (w *logWriter) String() string {
	return w.full.String()
}
