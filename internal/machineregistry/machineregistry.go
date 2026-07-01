package machineregistry

import (
	"context"
	"os"
	"runtime"

	"github.com/BramVR/codemesh/internal/state"
)

type Store interface {
	RegisterMachine(context.Context, state.MachineFacts) (state.Machine, error)
}

type Registry struct {
	Store Store
}

func (r Registry) Register(ctx context.Context, workspaceRoot string) (state.Machine, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return state.Machine{}, err
	}
	return r.Store.RegisterMachine(ctx, state.MachineFacts{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Architecture:  runtime.GOARCH,
		WorkspaceRoot: workspaceRoot,
	})
}
