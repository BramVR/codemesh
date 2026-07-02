package toolchain

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type Status string

const (
	StatusPresent Status = "present"
	StatusMissing Status = "missing"
	StatusUnknown Status = "unknown"
)

type Result struct {
	Name    string       `json:"name"`
	Status  Status       `json:"status"`
	Project ProjectFacts `json:"project"`
	Host    HostFacts    `json:"host"`
}

type ProjectFacts struct {
	Requirement string `json:"requirement"`
}

type HostFacts struct {
	Command string `json:"command,omitempty"`
	Version string `json:"version,omitempty"`
}

type Detection struct {
	Status  Status
	Command string
	Version string
}

type Detector interface {
	Detect(context.Context, string) (Status, error)
}

type DetailedDetector interface {
	DetectDetails(context.Context, string) (Detection, error)
}

type FakeDetector struct {
	Statuses map[string]Status
}

type UnknownDetector struct{}

type HostDetector struct {
	Lookup   func(string) (string, error)
	Version  func(context.Context, string) (string, error)
	DenyDirs []string
}

func Check(ctx context.Context, requirements []string, detector Detector) ([]Result, error) {
	names := uniqueSorted(requirements)
	if len(names) == 0 {
		return nil, nil
	}
	if detector == nil {
		detector = UnknownDetector{}
	}
	results := make([]Result, 0, len(names))
	for _, name := range names {
		detection, err := detect(ctx, detector, name)
		if err != nil {
			return nil, err
		}
		if !validStatus(detection.Status) {
			return nil, fmt.Errorf("toolchain requirement %s returned unsupported status %q", name, detection.Status)
		}
		results = append(results, Result{
			Name:    name,
			Status:  detection.Status,
			Project: ProjectFacts{Requirement: name},
			Host: HostFacts{
				Command: strings.TrimSpace(detection.Command),
				Version: strings.TrimSpace(detection.Version),
			},
		})
	}
	return results, nil
}

func detect(ctx context.Context, detector Detector, name string) (Detection, error) {
	if detailed, ok := detector.(DetailedDetector); ok {
		detection, err := detailed.DetectDetails(ctx, name)
		if err != nil {
			return Detection{}, err
		}
		return detection, nil
	}
	status, err := detector.Detect(ctx, name)
	if err != nil {
		return Detection{}, err
	}
	return Detection{Status: status}, nil
}

func (f FakeDetector) Detect(_ context.Context, name string) (Status, error) {
	if f.Statuses == nil {
		return StatusUnknown, nil
	}
	status, ok := f.Statuses[strings.TrimSpace(name)]
	if !ok {
		return StatusUnknown, nil
	}
	return status, nil
}

func (UnknownDetector) Detect(context.Context, string) (Status, error) {
	return StatusUnknown, nil
}

func (d HostDetector) Detect(ctx context.Context, name string) (Status, error) {
	detection, err := d.DetectDetails(ctx, name)
	if err != nil {
		return "", err
	}
	return detection.Status, nil
}

func (d HostDetector) DetectDetails(ctx context.Context, name string) (Detection, error) {
	command := strings.TrimSpace(name)
	if command == "" {
		return Detection{Status: StatusUnknown}, nil
	}
	if !ValidRequirementName(command) {
		return Detection{Status: StatusUnknown}, nil
	}
	lookup := d.Lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	resolved, err := lookup(command)
	if err != nil {
		return Detection{Status: StatusMissing, Command: command}, nil
	}
	if pathDenied(resolved, d.DenyDirs) {
		return Detection{Status: StatusUnknown, Command: command}, nil
	}
	versionRunner := d.Version
	var version string
	if versionRunner == nil {
		version, _ = runVersionCommand(ctx, command, resolved)
	} else {
		version, _ = versionRunner(ctx, command)
	}
	return Detection{Status: StatusPresent, Command: command, Version: firstLine(version)}, nil
}

func ValidRequirementName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	for i, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if i > 0 {
			switch r {
			case '-', '_', '.', '+':
				continue
			}
		}
		return false
	}
	return true
}

func runVersionCommand(ctx context.Context, command, executable string) (string, error) {
	args := versionArgs(command)
	output, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func versionArgs(command string) []string {
	switch command {
	case "go":
		return []string{"version"}
	default:
		return []string{"--version"}
	}
}

func validStatus(status Status) bool {
	switch status {
	case StatusPresent, StatusMissing, StatusUnknown:
		return true
	default:
		return false
	}
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func pathDenied(path string, dirs []string) bool {
	if strings.TrimSpace(path) == "" || len(dirs) == 0 {
		return false
	}
	if insideDeniedDir(path, dirs) {
		return true
	}
	if evaluated, err := filepath.EvalSymlinks(path); err == nil && evaluated != path {
		return insideDeniedDir(evaluated, dirs)
	}
	return false
}

func insideDeniedDir(path string, dirs []string) bool {
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dirAbs, err := filepath.Abs(dir)
		if err != nil {
			return true
		}
		if pathWithinDir(pathAbs, dirAbs) {
			return true
		}
		if evaluatedDir, err := filepath.EvalSymlinks(dirAbs); err == nil && evaluatedDir != dirAbs {
			if pathWithinDir(pathAbs, evaluatedDir) {
				return true
			}
		}
	}
	return false
}

func pathWithinDir(pathAbs, dirAbs string) bool {
	rel, err := filepath.Rel(dirAbs, pathAbs)
	if err != nil {
		return true
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
