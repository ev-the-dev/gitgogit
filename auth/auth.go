package auth

import (
	"fmt"

	"gitgogit/config"
)

// Env is a slice of KEY=VALUE strings to inject into a git subprocess environment.
type Env []string

// Provider resolves credentials for a remote endpoint.
type Provider interface {
	// Prepare returns the (possibly rewritten) remote URL and additional
	// environment variables to pass to the git subprocess.
	Prepare(rawURL string, cfg config.AuthConfig) (resolvedURL string, extraEnv Env, err error)
}

// Resolve returns the appropriate Provider for the given AuthConfig.
func Resolve(cfg config.AuthConfig) (Provider, error) {
	switch cfg.Type {
	case "ssh":
		return SSHProvider{}, nil
	case "token":
		return TokenProvider{}, nil
	case "":
		return NoAuthProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown auth type %q", cfg.Type)
	}
}

type NoAuthProvider struct{}

func (NoAuthProvider) Prepare(rawURL string, _ config.AuthConfig) (string, Env, error) {
	return rawURL, nil, nil
}
