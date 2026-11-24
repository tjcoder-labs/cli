package convsync

import (
	"regexp"
	"strings"
)

var (
	privateKeyRe = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	tokenRes     = []*regexp.Regexp{
		regexp.MustCompile(`\b(?:sk|pk|rk)-(?:proj-|ant-|live-|test-)?[A-Za-z0-9_-]{16,}\b`),
		regexp.MustCompile(`\bxai-[A-Za-z0-9_-]{16,}\b`),
		regexp.MustCompile(`\bek_[A-Za-z0-9_-]{16,}\b`),
		regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`),
		regexp.MustCompile(`\bre_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	}
	bearerRe  = regexp.MustCompile(`(?i)\b(Bearer\s+)([A-Za-z0-9._~+/=-]{16,})`)
	mcpHostRe = regexp.MustCompile(`(/mcp/hosts/)([A-Za-z0-9_-]{12,})`)
	assignRe  = regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(?:api[_-]?key|secret|token|password|passwd)[A-Za-z0-9_]*)(\s*[:=]\s*["']?)([^\s"'` + "`" + `,;]{8,})`)
)

// Mask keeps the first and last 4 characters of a secret.
func Mask(s string) string {
	if len(s) <= 12 {
		return "[REDACTED]"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

// Redact masks API keys, bearer tokens, private keys, JWTs, MCP capability
// URLs and `*_KEY=`/`password:`-style assignments in free text.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = privateKeyRe.ReplaceAllString(s, "[REDACTED PRIVATE KEY]")
	for _, re := range tokenRes {
		s = re.ReplaceAllStringFunc(s, Mask)
	}
	s = bearerRe.ReplaceAllStringFunc(s, func(m string) string {
		p := bearerRe.FindStringSubmatch(m)
		return p[1] + Mask(p[2])
	})
	s = mcpHostRe.ReplaceAllStringFunc(s, func(m string) string {
		p := mcpHostRe.FindStringSubmatch(m)
		return p[1] + Mask(p[2])
	})
	s = assignRe.ReplaceAllStringFunc(s, func(m string) string {
		p := assignRe.FindStringSubmatch(m)
		if strings.Contains(p[3], "…") || strings.HasPrefix(p[3], "[REDACTED") {
			return m
		}
		return p[1] + p[2] + Mask(p[3])
	})
	return s
}
