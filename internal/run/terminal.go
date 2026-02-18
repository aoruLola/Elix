package run

import "strings"

func deriveTerminalInfo(status string, errText string) TerminalInfo {
	switch status {
	case StatusCompleted:
		return TerminalInfo{
			IsTerminal: true,
			Outcome:    StatusCompleted,
			ReasonCode: "success",
			Reason:     "run completed successfully",
		}
	case StatusCancelled:
		return TerminalInfo{
			IsTerminal: true,
			Outcome:    StatusCancelled,
			ReasonCode: "cancelled_by_user",
			Reason:     "run was cancelled",
		}
	case StatusFailed:
		code := classifyFailureCode(errText)
		reason := strings.TrimSpace(errText)
		if reason == "" {
			reason = defaultFailureReason(code)
		}
		return TerminalInfo{
			IsTerminal: true,
			Outcome:    StatusFailed,
			ReasonCode: code,
			Reason:     reason,
		}
	default:
		return TerminalInfo{
			IsTerminal: false,
			ReasonCode: "in_progress",
			Reason:     status,
		}
	}
}

func classifyFailureCode(errText string) string {
	s := strings.ToLower(strings.TrimSpace(errText))
	switch {
	case s == "":
		return "backend_error"
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timeout"):
		return "timeout"
	case strings.Contains(s, "cancelled"), strings.Contains(s, "canceled"):
		return "cancelled"
	case strings.Contains(s, "invalid event contract"), strings.Contains(s, "schema_version"):
		return "contract_error"
	case strings.Contains(s, "workspace path"), strings.Contains(s, "outside allowed roots"), strings.Contains(s, "policy"):
		return "policy_denied"
	default:
		return "backend_error"
	}
}

func defaultFailureReason(code string) string {
	switch code {
	case "timeout":
		return "run timed out"
	case "cancelled":
		return "run was cancelled"
	case "contract_error":
		return "adapter emitted invalid event contract"
	case "policy_denied":
		return "run blocked by bridge policy"
	default:
		return "backend run failed"
	}
}
