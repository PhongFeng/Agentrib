package control

import (
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func validAgentStatuses(statuses map[string]protocol.AgentStatus) bool {
	if len(statuses) > len(protocol.Agents) {
		return false
	}
	for key, status := range statuses {
		if _, ok := protocol.FindAgent(key); !ok || len(status.Quotas) > 16 {
			return false
		}
		switch status.Account {
		case "", "available", "limited", "signed_in", "auth_required":
		default:
			return false
		}
		for _, quota := range status.Quotas {
			switch quota.Unit {
			case "percent", "credits", "requests", "tokens", "CNY", "USD":
			default:
				return false
			}
			if quota.Remaining < 0 || quota.Unit == "percent" && quota.Remaining > 100 ||
				quota.WindowMinutes < 0 || quota.ResetsAt < 0 || quota.ResetsAt > 253402300799 ||
				len(quota.Label) > 100 || strings.ContainsRune(quota.Label, 0) {
				return false
			}
		}
	}
	return true
}
