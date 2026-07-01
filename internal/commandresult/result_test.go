package commandresult

import "testing"

func TestReadinessExitClass(t *testing.T) {
	tests := []struct {
		name     string
		warnings int
		blockers int
		want     ExitClass
		code     int
	}{
		{name: "success", want: ExitSuccess, code: 0},
		{name: "warning", warnings: 1, want: ExitReadinessWarning, code: 0},
		{name: "blocked", blockers: 1, want: ExitReadinessBlocked, code: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReadinessExitClass(tt.warnings, tt.blockers)
			if got != tt.want {
				t.Fatalf("ReadinessExitClass() = %q, want %q", got, tt.want)
			}
			if got.Code() != tt.code {
				t.Fatalf("%s Code() = %d, want %d", got, got.Code(), tt.code)
			}
		})
	}
}

func TestNewNormalizesDiagnostics(t *testing.T) {
	result := New("status", ExitSuccess, Diagnostics{}, "payload")

	if result.Diagnostics.Warnings == nil {
		t.Fatal("warnings = nil, want empty slice")
	}
	if result.Diagnostics.Blockers == nil {
		t.Fatal("blockers = nil, want empty slice")
	}
}

func TestFailureExitClassCodes(t *testing.T) {
	if ExitUsageError.Code() != 2 {
		t.Fatalf("usage Code() = %d, want 2", ExitUsageError.Code())
	}
	if ExitInternalError.Code() != 1 {
		t.Fatalf("internal Code() = %d, want 1", ExitInternalError.Code())
	}
}
