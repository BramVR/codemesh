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

type RegisterOptions struct {
	Name          string
	CodeMeshHome  string
	WorkspaceRoot string
}

func (r Registry) Register(ctx context.Context, opts RegisterOptions) (state.Machine, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return state.Machine{}, err
	}
	return r.Store.RegisterMachine(ctx, state.MachineFacts{
		Name:          opts.Name,
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Architecture:  runtime.GOARCH,
		CodeMeshHome:  opts.CodeMeshHome,
		WorkspaceRoot: opts.WorkspaceRoot,
	})
}
