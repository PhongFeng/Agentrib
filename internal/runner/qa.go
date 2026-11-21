package runner

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

var errQARejected = errors.New("本轮验收未通过，已记录缺陷单")

// QA reports can include eight structured tickets with evidence and JSON escapes.
const maxQAReplyBytes = 128 << 10

const qaReworkInstructions = `

验收对象是 CI 给出的原始版本。可以写临时验收脚本，但不要修改游戏代码、素材、测试标准或提交变更。
需要浏览器实测的条款，须实际启动该版本的网页包并进行交互验证。无法验证时标为失败，缺少运行工具时归属 environment。
若首行是 QA: FAIL，从第二行起只输出一个 JSON 对象，格式为
{"bugs":[{"owner":"audio","title":"缺陷标题","steps":"复现步骤","expected":"预期结果","actual":"实际结果与证据"}]}
owner 只能是 art（美术）、audio（音效）、develop（开发）、environment（执行环境）、producer（需求有歧义或需要产品分诊）。每单一个主责，最多 8 单。复验时仍存在的问题在 bug 中填写原 id，新问题省略 id。在同一 JSON 对象的 verified 数组列出本次已实测通过的旧单编号，未验证的不能关闭。
标题最多 240 UTF-8 字节，steps、expected、actual 各最多 900 UTF-8 字节。写必要的复现和证据摘要，省略完整日志与重复信息。QA: PASS 的正文也只需汇总验收结果和关键证据。
`

func reviewRevision(result protocol.Result, reply string, emit Emit) (protocol.Result, error) {
	emit("qa", reply)
	verdict, body, _ := strings.Cut(reply, "\n")
	switch strings.TrimSpace(verdict) {
	case "QA: PASS":
		return result, nil
	case "QA: FAIL":
		body = strings.TrimSpace(body)
		if strings.HasPrefix(body, "```json") && strings.HasSuffix(body, "```") {
			body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body, "```json"), "```"))
		}
		var report struct {
			Bugs     []protocol.Bug
			Verified []string
		}
		if json.Unmarshal([]byte(body), &report) != nil || !protocol.ValidBugs(report.Bugs) || !protocol.ValidVerified(report.Verified, report.Bugs) {
			return result, errors.New("QA 验收未通过，缺陷单格式无效")
		}
		result.Verified = report.Verified
		result.Bugs = report.Bugs
		return result, errQARejected
	default:
		return result, errors.New("QA 没有给出明确的验收结论")
	}
}
