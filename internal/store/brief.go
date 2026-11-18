package store

import (
	"context"
	"errors"
	"slices"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

var ErrBriefUnavailable = errors.New("执行机器尚未启用产品 Agent 对话")

func (s *Store) CreateBriefRun(ctx context.Context, id string, spec protocol.Spec) (protocol.Run, error) {
	machine, err := scanMachine(s.db.QueryRow(ctx, `SELECT `+machineColumns+` FROM machines WHERE id=$1`, spec.MachineID))
	if err != nil {
		return protocol.Run{}, err
	}
	if !machine.BriefChat {
		return protocol.Run{}, ErrBriefUnavailable
	}
	if !slices.Contains(machine.Executors, spec.Executor) {
		return protocol.Run{}, ErrAgentUnavailable
	}
	if !slices.Contains(machine.Projects, spec.Project) {
		return protocol.Run{}, ErrProjectEngine
	}
	engine, known := protocol.FindEngine(protocol.EngineID(machine.ProjectEngines[spec.Project]))
	if !known || !engine.Implemented {
		return protocol.Run{}, ErrEngineUnavailable
	}
	spec.Engine = engine.ID
	return s.CreateRun(ctx, id, spec)
}
