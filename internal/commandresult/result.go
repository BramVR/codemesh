package commandresult

type ExitClass string

const (
	ExitSuccess          ExitClass = "success"
	ExitReadinessWarning ExitClass = "readiness-warning"
	ExitReadinessBlocked ExitClass = "readiness-blocked"
	ExitUsageError       ExitClass = "usage-error"
	ExitInternalError    ExitClass = "internal-error"
)

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

type Diagnostics struct {
	Warnings []Diagnostic `json:"warnings"`
	Blockers []Diagnostic `json:"blockers"`
}

type Result[T any] struct {
	Command     string      `json:"command"`
	ExitClass   ExitClass   `json:"exit_class"`
	Diagnostics Diagnostics `json:"diagnostics"`
	Payload     T           `json:"payload"`
}

func New[T any](command string, exitClass ExitClass, diagnostics Diagnostics, payload T) Result[T] {
	diagnostics = normalizeDiagnostics(diagnostics)
	return Result[T]{
		Command:     command,
		ExitClass:   exitClass,
		Diagnostics: diagnostics,
		Payload:     payload,
	}
}

func normalizeDiagnostics(diagnostics Diagnostics) Diagnostics {
	if diagnostics.Warnings == nil {
		diagnostics.Warnings = []Diagnostic{}
	}
	if diagnostics.Blockers == nil {
		diagnostics.Blockers = []Diagnostic{}
	}
	return diagnostics
}

func ReadinessExitClass(warnings, blockers int) ExitClass {
	if blockers > 0 {
		return ExitReadinessBlocked
	}
	if warnings > 0 {
		return ExitReadinessWarning
	}
	return ExitSuccess
}

func (c ExitClass) Code() int {
	switch c {
	case ExitUsageError:
		return 2
	case ExitInternalError:
		return 1
	default:
		return 0
	}
}
