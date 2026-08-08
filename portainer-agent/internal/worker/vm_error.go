package worker

import (
	"strings"
)

// maxAppServerErrorLen caps what is stored on app_servers.error_message. The
// console renders that column verbatim under the server name.
const maxAppServerErrorLen = 240

// friendlyVMError condenses a provisioning/teardown failure into the one line
// the console shows next to the server.
//
// Terraform failures arrive as a whole rendered diagnostic: an `exit status 1`,
// a summary, the source location (`with <resource>`, `on main.tf line 34`, the
// echoed source line) and only then the sentence that says what actually went
// wrong. Stored raw, the console printed all of it on one wrapped line, so the
// user read a stack of file coordinates before reaching "Region 'eu1' does not
// exist. Available regions: ru1".
//
// The full text is not lost: it stays on the operation record and in the agent
// logs, which is where the coordinates are worth having.
func friendlyVMError(err error) string {
	if err == nil {
		return ""
	}
	return summarizeTerraformError(err.Error())
}

// summarizeTerraformError extracts `Error: <summary>` plus the first line of
// its explanation, falling back to the first non-empty line for errors that
// did not come from Terraform.
func summarizeTerraformError(raw string) string {
	lines := strings.Split(raw, "\n")

	summary, detail := "", ""
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if !strings.HasPrefix(text, "Error: ") {
			continue
		}
		summary = strings.TrimSpace(strings.TrimPrefix(text, "Error: "))
		for _, rest := range lines[i+1:] {
			candidate := strings.TrimSpace(rest)
			if candidate == "" || isTerraformLocationLine(candidate) {
				continue
			}
			detail = candidate
			break
		}
		break
	}

	if summary == "" {
		for _, line := range lines {
			if text := strings.TrimSpace(line); text != "" {
				summary = text
				break
			}
		}
	}

	message := summary
	if detail != "" && !strings.EqualFold(detail, summary) {
		message = summary + ": " + detail
	}
	return truncateError(message)
}

// isTerraformLocationLine matches the source-coordinate lines Terraform prints
// between a diagnostic's summary and its explanation.
func isTerraformLocationLine(text string) bool {
	if strings.HasPrefix(text, "with ") || strings.HasPrefix(text, "on ") {
		return true
	}
	digits := 0
	for _, r := range text {
		if r >= '0' && r <= '9' {
			digits++
			continue
		}
		return r == ':' && digits > 0
	}
	return false
}

// truncateError clips a message to maxAppServerErrorLen runes.
func truncateError(message string) string {
	runes := []rune(message)
	if len(runes) <= maxAppServerErrorLen {
		return message
	}
	return string(runes[:maxAppServerErrorLen-1]) + "…"
}
