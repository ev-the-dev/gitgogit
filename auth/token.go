package auth

import (
	"fmt"
	"net/url"
	"os"

	"gitgogit/config"
)

// TokenProvider implements Provider for HTTPS token authentication.
// It injects the token into the URL as https://oauth2:TOKEN@host/path.
type TokenProvider struct{}

// InjectToken rewrites an HTTP/HTTPS URL to include OAuth2 credentials.
// The token is read at call time so that environment variable rotation works without a config reload.
func InjectToken(rawURL, token string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("token auth requires an http/https URL, got scheme %q", u.Scheme)
	}
	if token == "" {
		return "", fmt.Errorf("token is empty")
	}
	u.User = url.UserPassword("oauth2", token)
	return u.String(), nil
}

func (TokenProvider) Prepare(rawURL string, cfg config.AuthConfig) (string, Env, error) {
	if cfg.Env == "" {
		return rawURL, nil, fmt.Errorf("token auth: env var name is required")
	}
	token := os.Getenv(cfg.Env)
	if token == "" {
		return rawURL, nil, fmt.Errorf("token auth: env var %q is not set or empty", cfg.Env)
	}
	resolved, err := InjectToken(rawURL, token)
	if err != nil {
		return rawURL, nil, err
	}
	return resolved, nil, nil
}
