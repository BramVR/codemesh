package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvHome      = "CODEMESH_HOME"
	DatabaseName = "codemesh.db"
	AgentsDir    = "agents"
)

type Paths struct {
	Home      string
	Database  string
	AgentsDir string
}

func ResolvePaths() (Paths, error) {
	home, err := ResolveHome()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Home:      home,
		Database:  filepath.Join(home, DatabaseName),
		AgentsDir: filepath.Join(home, AgentsDir),
	}, nil
}

func ResolveHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvHome)); override != "" {
		return cleanAbs(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if home == "" {
		return "", errors.New("user home directory is empty")
	}
	return filepath.Join(home, ".codemesh"), nil
}

func ResolveWorkspaceRoot(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return cwd, nil
	}
	return cleanAbs(arg)
}

func cleanAbs(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}
