package runner

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func (r *Runner) pollAgentStatuses(ctx context.Context, installed map[string]protocol.AgentStatus,
	updates chan<- map[string]protocol.AgentStatus) {
	for ctx.Err() == nil {
		statuses := make(map[string]protocol.AgentStatus, len(installed))
		for _, agent := range protocol.Agents {
			status, found := installed[agent.ID]
			if !found || ctx.Err() != nil {
				continue
			}
			check, cancel := context.WithTimeout(ctx, 12*time.Second)
			fresh := agentUsage(check, agent.ID, r.agentPaths[agent.ID])
			cancel()
			if fresh.Account != "" || len(fresh.Quotas) > 0 {
				status = fresh
			}
			statuses[agent.ID] = status
		}
		select {
		case updates <- statuses:
		case <-ctx.Done():
			return
		}
		sleep(ctx, 2*time.Minute)
	}
}

func agentUsage(ctx context.Context, id, path string) protocol.AgentStatus {
	switch id {
	case "codex":
		return codexUsage(ctx, path)
	case "qoder-cn":
		out, err := command(ctx, "", path, "status", "--output", "json").Output()
		var account struct {
			LoggedIn *bool `json:"logged_in"`
		}
		if err == nil && json.Unmarshal(out, &account) == nil && account.LoggedIn != nil {
			if *account.LoggedIn {
				return protocol.AgentStatus{Account: "signed_in"}
			}
			return protocol.AgentStatus{Account: "auth_required"}
		}
	case "deepseek-harness":
		return deepSeekUsage(ctx, strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")))
	}
	return protocol.AgentStatus{}
}

func codexUsage(ctx context.Context, path string) protocol.AgentStatus {
	cmd := command(ctx, "", path, "app-server", "--listen", "stdio://")
	input, err := cmd.StdinPipe()
	if err != nil {
		return protocol.AgentStatus{}
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return protocol.AgentStatus{}
	}
	defer func() {
		_ = input.Close()
		_ = cmd.Wait()
	}()
	const requests = `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"agentrib","version":"0.1.0"}}}
{"method":"initialized"}
{"id":2,"method":"account/rateLimits/read"}
`
	if _, err := io.WriteString(input, requests); err != nil {
		return protocol.AgentStatus{}
	}
	return readCodexUsage(io.LimitReader(output, 2<<20))
}

type codexUsageWindow struct {
	Used    *float64 `json:"usedPercent"`
	Minutes int      `json:"windowDurationMins"`
	Resets  int64    `json:"resetsAt"`
}

type codexLimits struct {
	Primary   *codexUsageWindow `json:"primary"`
	Secondary *codexUsageWindow `json:"secondary"`
	Reached   string            `json:"rateLimitReachedType"`
	Credits   *struct {
		Balance json.RawMessage `json:"balance"`
	} `json:"credits"`
}

func readCodexUsage(output io.Reader) protocol.AgentStatus {
	decoder := json.NewDecoder(output)
	for {
		var reply struct {
			ID     json.RawMessage `json:"id"`
			Result struct {
				Limits  *codexLimits           `json:"rateLimits"`
				ByLimit map[string]codexLimits `json:"rateLimitsByLimitId"`
			} `json:"result"`
		}
		if decoder.Decode(&reply) != nil {
			return protocol.AgentStatus{}
		}
		if string(reply.ID) != "2" {
			continue
		}
		limits := reply.Result.Limits
		if main, ok := reply.Result.ByLimit["codex"]; ok {
			limits = &main
		}
		if limits == nil {
			return protocol.AgentStatus{}
		}
		status := protocol.AgentStatus{Account: "signed_in"}
		for _, window := range []*codexUsageWindow{limits.Primary, limits.Secondary} {
			if window == nil || window.Used == nil || *window.Used < 0 || window.Minutes < 0 || window.Resets < 0 || window.Resets > 253402300799 {
				continue
			}
			status.Quotas = append(status.Quotas, protocol.AgentQuota{Unit: "percent", Remaining: max(0, 100-*window.Used),
				WindowMinutes: window.Minutes, ResetsAt: window.Resets})
		}
		if len(status.Quotas) > 0 {
			status.Account = "available"
		}
		if limits.Reached != "" {
			status.Account = "limited"
		} else if limits.Credits == nil {
			for _, quota := range status.Quotas {
				if quota.Remaining == 0 {
					status.Account = "limited"
				}
			}
		}
		if limits.Credits != nil {
			balance := strings.Trim(string(limits.Credits.Balance), `"`)
			if amount, ok := quotaAmount(balance); ok {
				status.Quotas = append(status.Quotas, protocol.AgentQuota{Unit: "credits", Remaining: amount, Label: "额外额度"})
			}
		}
		return status
	}
}

func deepSeekUsage(ctx context.Context, key string) protocol.AgentStatus {
	if key == "" {
		return protocol.AgentStatus{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.deepseek.com/user/balance", nil)
	if err != nil {
		return protocol.AgentStatus{}
	}
	request.Header.Set("Authorization", "Bearer "+key)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return protocol.AgentStatus{}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return protocol.AgentStatus{Account: "auth_required"}
	}
	var data struct {
		Available *bool `json:"is_available"`
		Balances  []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&data) != nil || data.Available == nil {
		return protocol.AgentStatus{}
	}
	status := protocol.AgentStatus{Account: "limited"}
	if *data.Available {
		status.Account = "available"
	}
	seen := make(map[string]bool)
	for _, balance := range data.Balances {
		if amount, ok := quotaAmount(balance.Total); ok && !seen[balance.Currency] && (balance.Currency == "USD" || balance.Currency == "CNY") {
			status.Quotas = append(status.Quotas, protocol.AgentQuota{Label: "API 余额", Unit: balance.Currency, Remaining: amount})
			seen[balance.Currency] = true
		}
	}
	return status
}

func quotaAmount(value string) (float64, bool) {
	n, err := strconv.ParseFloat(value, 64)
	return n, err == nil && n >= 0 && !math.IsInf(n, 0)
}
