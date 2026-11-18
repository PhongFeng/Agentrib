package runner

import (
	"strings"
	"testing"
)

func TestCodexUsageReportsProviderWindows(t *testing.T) {
	const response = `{"id":1,"result":{"userAgent":"codex"}}
{"method":"account/rateLimits/updated","params":{}}
{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":80}},"rateLimitsByLimitId":{"codex":{
  "primary":{"usedPercent":25,"windowDurationMins":300,"resetsAt":1730947200},
  "secondary":{"usedPercent":100,"windowDurationMins":10080,"resetsAt":1730947201},
  "rateLimitReachedType":"rate_limit_reached",
  "credits":{"balance":"17.5"}
}}}}
`
	status := readCodexUsage(strings.NewReader(response))
	if status.Account != "limited" || len(status.Quotas) != 3 {
		t.Fatalf("unexpected usage: %+v", status)
	}
	if first := status.Quotas[0]; first.Remaining != 75 || first.Unit != "percent" || first.WindowMinutes != 300 || first.ResetsAt != 1730947200 {
		t.Fatalf("incorrect primary window: %+v", first)
	}
	if second := status.Quotas[1]; second.Remaining != 0 || second.Unit != "percent" || second.WindowMinutes != 10080 {
		t.Fatalf("incorrect secondary window: %+v", second)
	}
	if credits := status.Quotas[2]; credits.Remaining != 17.5 || credits.Unit != "credits" {
		t.Fatalf("incorrect extra credits: %+v", credits)
	}
}

func TestCodexUsageReportsUnknownOnLookupFailure(t *testing.T) {
	const response = `{"id":2,"error":{"code":-32603,"message":"Provider unreachable"}}`
	status := readCodexUsage(strings.NewReader(response))
	if status.Account != "" || len(status.Quotas) > 0 {
		t.Fatalf("lookup failure was reported as usable quota: %+v", status)
	}
}
