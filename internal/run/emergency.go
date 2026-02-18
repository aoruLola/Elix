package run

import (
	"context"
	"strings"
	"time"

	"echohelix/internal/driver"
	"echohelix/internal/events"
)

type emergencyTarget struct {
	runID      string
	backend    string
	driver     driver.Driver
	cancelFunc context.CancelFunc
}

func (s *Service) EmergencyStatus() EmergencyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emergency
}

func (s *Service) isEmergencyActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emergency.Active
}

func (s *Service) EmergencyStop(ctx context.Context, reason string) (EmergencyState, int) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "manual emergency stop"
	}
	now := time.Now().UTC()

	targets := make([]emergencyTarget, 0, 16)
	s.mu.Lock()
	s.emergency = EmergencyState{
		Active:    true,
		Reason:    reason,
		Activated: now,
	}
	for runID, ar := range s.active {
		targets = append(targets, emergencyTarget{
			runID:      runID,
			backend:    ar.backend,
			driver:     ar.driver,
			cancelFunc: ar.cancel,
		})
	}
	s.mu.Unlock()

	for _, target := range targets {
		backend := target.backend
		if backend == "" {
			backend = "unknown"
		}
		s.setStatus(ctx, target.runID, StatusCancelling, "")
		s.emit(ctx, target.runID, backend, "bridge", events.TypeStatus, map[string]any{
			"status": StatusCancelling,
			"reason": "emergency_stop",
		})
		if target.driver != nil {
			_ = target.driver.Cancel(ctx, target.runID)
		}
		if target.cancelFunc != nil {
			target.cancelFunc()
		}
		s.setStatus(ctx, target.runID, StatusCancelled, "")
		s.emit(ctx, target.runID, backend, "bridge", events.TypeStatus, map[string]any{
			"status": StatusCancelled,
			"reason": "emergency_stop",
		})
	}
	return s.EmergencyStatus(), len(targets)
}

func (s *Service) EmergencyResume() EmergencyState {
	s.mu.Lock()
	s.emergency = EmergencyState{}
	state := s.emergency
	s.mu.Unlock()
	return state
}
