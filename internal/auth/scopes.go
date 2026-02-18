package auth

const (
	ScopeRunsSubmit   = "runs:submit"
	ScopeRunsRead     = "runs:read"
	ScopeRunsCancel   = "runs:cancel"
	ScopeBackendsRead = "backends:read"
	ScopePairStart    = "pair:start"
	ScopeDevicesRead  = "devices:read"
	ScopeDevicesWrite = "devices:write"
)

var allScopes = map[string]struct{}{
	ScopeRunsSubmit:   {},
	ScopeRunsRead:     {},
	ScopeRunsCancel:   {},
	ScopeBackendsRead: {},
	ScopePairStart:    {},
	ScopeDevicesRead:  {},
	ScopeDevicesWrite: {},
}

func defaultScopes() []string {
	return []string{
		ScopeRunsSubmit,
		ScopeRunsRead,
		ScopeRunsCancel,
		ScopeBackendsRead,
		ScopeDevicesRead,
		ScopeDevicesWrite,
	}
}

func staticBootstrapScopes() []string {
	return []string{
		ScopePairStart,
		ScopeBackendsRead,
		ScopeDevicesRead,
		ScopeDevicesWrite,
	}
}

func normalizeScopes(in []string) []string {
	if len(in) == 0 {
		out := make([]string, 0, len(defaultScopes()))
		out = append(out, defaultScopes()...)
		return out
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := allScopes[s]; !ok {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		out = append(out, defaultScopes()...)
	}
	return out
}
