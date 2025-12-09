package compose

import (
	"bytes"
	_ "embed"
	"net/url"
	"regexp"
	"text/template"
)

//go:embed templates/install.sh
var installScriptTemplate string

// InstallScriptOptions contains options for generating an install script
type InstallScriptOptions struct {
	RepoName      string   // Repository name
	BaseURL       string   // Base URL for the repository
	Distributions []string // Available distributions
	KeyringName   string   // Keyring filename (sanitized domain)
}

var nonAlphanumericRegex = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// GenerateKeyringName creates a keyring filename from base URL
// Format: <sanitized-domain>
func GenerateKeyringName(baseURL string) string {
	// Parse URL to extract domain
	u, err := url.Parse(baseURL)
	if err != nil {
		// If parsing fails, use the raw baseURL
		u = &url.URL{Host: baseURL}
	}

	// Extract domain (host without port)
	domain := u.Host
	if domain == "" {
		domain = baseURL
	}

	// Sanitize domain: replace non-alphanumeric with dashes
	return nonAlphanumericRegex.ReplaceAllString(domain, "-")
}

// GenerateInstallScript generates a bash script for installing the repository
func GenerateInstallScript(opts InstallScriptOptions) (string, error) {
	tmpl, err := template.New("install").Parse(installScriptTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, opts); err != nil {
		return "", err
	}

	return buf.String(), nil
}
