package sanitize

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

var (
	secretKindRe = regexp.MustCompile(`(?m)kind:\s*Secret`)

	base64Re = regexp.MustCompile(`(?m)^(\s+[a-zA-Z0-9_-]+:\s*["']?)[A-Za-z0-9+/]{40,}={0,2}(["']?\s*)$`)

	jwtRe     = regexp.MustCompile(`(^|\s|["'])ey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]*([\s"']|$)`)
	bearerRe  = regexp.MustCompile(`(?i)(bearer\s+)[a-zA-Z0-9_-]{10,}`)
	sshKeyRe  = regexp.MustCompile(`(?s)-----BEGIN[^\n]*PRIVATE KEY-----.*?-----END[^\n]*PRIVATE KEY-----`)
	authHdrRe = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[a-zA-Z0-9_-]{10,}`)
	passwdRe  = regexp.MustCompile(`(?i)(password|pwd|secret)=[-a-zA-Z0-9_%]+`)
)

type Result struct {
	FilesProcessed int
	ItemsSanitized int
}

func Run(dir string, interruptCh <-chan struct{}) Result {
	log.Info("Starting data sanitization on directory: %s", dir)

	if _, err := os.Stat(dir); err != nil {
		log.Warn("Directory does not exist: %s", dir)
		return Result{}
	}

	var res Result
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if interruptCh != nil {
			select {
			case <-interruptCh:
				log.Warn("Sanitization interrupted, stopping walk")
				return filepath.SkipAll
			default:
			}
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".yaml", ".yml", ".json":
			sanitizeStructured(path, &res)
		case ".txt", ".log":
			sanitizeText(path, &res)
		}
		return nil
	})

	writeReport(dir, &res)

	log.Info("Sanitization complete!")
	log.Info("Files processed: %d", res.FilesProcessed)
	log.Info("Items sanitized: %d", res.ItemsSanitized)
	return res
}

func sanitizeStructured(path string, res *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	res.FilesProcessed++
	content := string(data)
	changed := false

	if secretKindRe.MatchString(content) {
		content = redactSecretData(content)
		changed = true
		res.ItemsSanitized++
	} else if base64Re.MatchString(content) {
		content = base64Re.ReplaceAllString(content, "${1}[REDACTED - Base64 data removed]${2}")
		changed = true
		res.ItemsSanitized++
	}

	if jwtRe.MatchString(content) {
		content = jwtRe.ReplaceAllString(content, "${1}[REDACTED-JWT-TOKEN]${2}")
		changed = true
		res.ItemsSanitized++
	}

	if bearerRe.MatchString(content) {
		content = bearerRe.ReplaceAllString(content, "${1}[REDACTED-BEARER-TOKEN]")
		changed = true
		res.ItemsSanitized++
	}

	if sshKeyRe.MatchString(content) {
		content = sshKeyRe.ReplaceAllString(content, "# [REDACTED - SSH Private Key Removed]")
		changed = true
		res.ItemsSanitized++
	}

	if changed {
		_ = os.WriteFile(path, []byte(content), 0o644)
	}
}

func sanitizeText(path string, res *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	res.FilesProcessed++
	content := string(data)
	changed := false

	if jwtRe.MatchString(content) {
		content = jwtRe.ReplaceAllString(content, "${1}[REDACTED-JWT-TOKEN]${2}")
		changed = true
		res.ItemsSanitized++
	}

	if authHdrRe.MatchString(content) {
		content = authHdrRe.ReplaceAllString(content, "${1}[REDACTED-TOKEN]")
		changed = true
		res.ItemsSanitized++
	}

	if passwdRe.MatchString(content) {
		content = passwdRe.ReplaceAllString(content, "${1}=[REDACTED]")
		changed = true
		res.ItemsSanitized++
	}

	if changed {
		_ = os.WriteFile(path, []byte(content), 0o644)
	}
}

// redactSecretData handles the multi-level indentation patterns for K8s Secret
// data blocks, matching the bash sed patterns.
func redactSecretData(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))

	type dataBlock struct {
		baseIndent int
		kvIndent   int
	}
	var inBlock *dataBlock

	for _, line := range lines {
		if inBlock != nil {
			trimmed := strings.TrimLeft(line, " ")
			indent := len(line) - len(trimmed)

			if indent <= inBlock.baseIndent && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				inBlock = nil
			} else if indent == inBlock.kvIndent && strings.Contains(trimmed, ": ") {
				key := trimmed[:strings.Index(trimmed, ": ")]
				line = strings.Repeat(" ", indent) + key + ": [REDACTED]"
			}
		}

		if inBlock == nil {
			trimmed := strings.TrimLeft(line, " ")
			indent := len(line) - len(trimmed)
			if trimmed == "data:" {
				inBlock = &dataBlock{baseIndent: indent, kvIndent: indent + 2}
			}
		}

		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func writeReport(dir string, res *Result) {
	report := filepath.Join(dir, "sanitization-report.txt")
	var sb strings.Builder

	fmt.Fprintf(&sb, "RHDH Must-Gather Data Sanitization Report\n")
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&sb, "Target Directory: %s\n", dir)
	sb.WriteString("\nThis report details what sensitive information was sanitized from the collected data.\n")
	sb.WriteString("\nSANITIZATION RULES APPLIED:\n")
	sb.WriteString("- Kubernetes Secret data values\n")
	sb.WriteString("- Base64 encoded sensitive data\n")
	sb.WriteString("- JWT tokens and bearer tokens\n")
	sb.WriteString("- Passwords and API keys\n")
	sb.WriteString("- Database connection strings\n")
	sb.WriteString("- URLs with embedded credentials\n")
	sb.WriteString("- SSH keys and certificates\n")
	sb.WriteString("- OAuth tokens and client secrets\n")
	fmt.Fprintf(&sb, "\nSANITIZATION SUMMARY:\n")
	fmt.Fprintf(&sb, "- Files processed: %d\n", res.FilesProcessed)
	fmt.Fprintf(&sb, "- Items sanitized: %d\n", res.ItemsSanitized)
	sb.WriteString("\nIMPORTANT: Please review the sanitized files before sharing externally to ensure\n")
	sb.WriteString("all sensitive information has been properly redacted.\n")

	_ = os.WriteFile(report, []byte(sb.String()), 0o644)
	log.Info("Report saved to: %s", report)
}
