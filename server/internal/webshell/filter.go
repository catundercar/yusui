// Package webshell is the v0.1 server-side SSH proxy: it holds the PTY, runs an
// SSH client to the asset, broadcasts to WebSocket attachers, filters dangerous
// commands, and records the session as asciinema (docs/09).
package webshell

import "regexp"

// Severity of a command-filter rule.
type Severity string

const (
	SevWarn    Severity = "warn"
	SevConfirm Severity = "confirm"
	SevBlock   Severity = "block"
)

func sevRank(s Severity) int {
	switch s {
	case SevWarn:
		return 1
	case SevConfirm:
		return 2
	case SevBlock:
		return 3
	default:
		return 0
	}
}

// Rule is one compiled command-filter pattern.
type Rule struct {
	ID       string
	Re       *regexp.Regexp
	Severity Severity
	Message  string
}

// Decision is the strictest rule that matched a command line.
type Decision struct {
	Action   string // pass | warn | confirm | block
	RuleID   string
	Severity Severity
	Message  string
}

// Ruleset holds the effective rules (global ∪ ai-stricter). docs/09 §9.7.
//
// HONEST LIMITATION (docs/09 §9.7.1): matching is on a stdin-reconstructed line
// (printable + backspace), so shell line-editing (^W/^U/history/tab) and
// alias/encode/heredoc bypass it. This defends against typos, not malicious
// users with shell access. v0.2+ moves to remote prompt-echo parsing.
type Ruleset struct {
	base []Rule
	ai   []Rule
}

// Match returns the strictest decision for line; source=="api" adds AI rules.
func (rs *Ruleset) Match(line, source string) Decision {
	d := Decision{Action: "pass"}
	eval := func(rules []Rule) {
		for i := range rules {
			r := rules[i]
			if r.Re.MatchString(line) && sevRank(r.Severity) > sevRank(d.Severity) {
				d = Decision{Action: string(r.Severity), RuleID: r.ID, Severity: r.Severity, Message: r.Message}
			}
		}
	}
	eval(rs.base)
	if source == "api" {
		eval(rs.ai)
	}
	return d
}

// Rules returns a JSON-able snapshot for the session policy snapshot.
func (rs *Ruleset) RuleIDs() []string {
	ids := make([]string, 0, len(rs.base)+len(rs.ai))
	for _, r := range rs.base {
		ids = append(ids, r.ID)
	}
	for _, r := range rs.ai {
		ids = append(ids, "ai:"+r.ID)
	}
	return ids
}

func mustRule(id, pat string, sev Severity, msg string) Rule {
	return Rule{ID: id, Re: regexp.MustCompile(pat), Severity: sev, Message: msg}
}

// DefaultRuleset is the built-in v0.1 rule set (docs/09 §9.7.5/§9.7.6).
func DefaultRuleset() *Ruleset {
	return &Ruleset{
		base: []Rule{
			mustRule("prevent-rm-rf-absolute",
				`(^|[;&|]\s*)\s*(sudo\s+)?rm\s+(-[a-zA-Z]*[rR][a-zA-Z]*\s+)*-[a-zA-Z]*[rR][a-zA-Z]*\s+(/(\s|$)|/(etc|var|usr|home|root|opt|bin|lib|boot)(/|\s|$))`,
				SevBlock, "禁止 rm -rf 系统关键路径"),
			mustRule("prevent-rm-rf-tilde-home", `\brm\s+(-[a-zA-Z]*[rR][a-zA-Z]*\s+).*(~|\$HOME)(/|\s|$)`,
				SevConfirm, "删除家目录需确认"),
			mustRule("confirm-dd-of-dev", `\bdd\b[^|]*\bof=/dev/`, SevConfirm, "dd 写入块设备会破坏数据，确认继续？"),
			mustRule("block-mkfs", `\bmkfs(\.[a-z0-9]+)?\b`, SevBlock, "格式化文件系统已阻止"),
			mustRule("block-forkbomb", `:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:[^}]*\}\s*;\s*:`, SevBlock, "Fork bomb 模式"),
			mustRule("confirm-shutdown", `\b(shutdown|reboot|poweroff|halt|init\s+0|init\s+6)\b`, SevConfirm, "停机命令需确认"),
			mustRule("confirm-iptables-flush", `\b(iptables\s+-F|nft\s+flush)\b`, SevConfirm, "清空防火墙规则需确认"),
			mustRule("warn-curl-pipe-bash", `\bcurl\b[^|]*\|\s*(sudo\s+)?(bash|sh|zsh)\b`, SevWarn, "下载即执行存在风险，已记录"),
			mustRule("warn-chmod-777", `\bchmod\s+(-R\s+)?0?777\b`, SevWarn, "chmod 777 已记录"),
		},
		ai: []Rule{
			mustRule("ai-confirm-any-rm", `\brm\b`, SevConfirm, "AI 执行 rm 需人工确认"),
			mustRule("ai-confirm-any-sudo", `\bsudo\b`, SevConfirm, "AI 执行 sudo 需人工确认"),
			mustRule("ai-block-pkg-mgr", `\b(apt|apt-get|yum|dnf|pip|pip3|npm)\s+(install|add)\b`, SevBlock, "AI 安装软件包已阻止"),
		},
	}
}
