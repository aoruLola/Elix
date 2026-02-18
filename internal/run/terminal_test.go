package run

import "testing"

func TestDeriveTerminalInfoCompleted(t *testing.T) {
	ti := deriveTerminalInfo(StatusCompleted, "")
	if !ti.IsTerminal || ti.Outcome != StatusCompleted || ti.ReasonCode != "success" {
		t.Fatalf("unexpected terminal info: %#v", ti)
	}
}

func TestDeriveTerminalInfoFailedTimeout(t *testing.T) {
	ti := deriveTerminalInfo(StatusFailed, "context deadline exceeded")
	if !ti.IsTerminal || ti.Outcome != StatusFailed || ti.ReasonCode != "timeout" {
		t.Fatalf("unexpected terminal info: %#v", ti)
	}
}

func TestDeriveTerminalInfoFailedPolicy(t *testing.T) {
	ti := deriveTerminalInfo(StatusFailed, "workspace path is outside allowed roots")
	if ti.ReasonCode != "policy_denied" {
		t.Fatalf("unexpected reason_code: %#v", ti)
	}
}

func TestDeriveTerminalInfoRunning(t *testing.T) {
	ti := deriveTerminalInfo(StatusRunning, "")
	if ti.IsTerminal {
		t.Fatalf("expected non-terminal for running, got %#v", ti)
	}
	if ti.ReasonCode != "in_progress" {
		t.Fatalf("unexpected reason_code for running: %#v", ti)
	}
}
