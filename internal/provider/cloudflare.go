package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dionysius/aarg/internal/config"
	"github.com/dionysius/aarg/internal/feed"
	"github.com/zeebo/blake3"
)

// PagesProvider implements the provider.Provider interface for Cloudflare Pages.
// It handles deployment using the Cloudflare Direct Upload API.
type PagesProvider struct {
	accountID     string
	projectName   string
	apiToken      string
	httpClient    *http.Client
	cleanupConfig CloudflareCleanupConfig
	repositories  []*config.RepositoryConfig
	poolMode      string
}

// CloudflareCleanupConfig contains deployment cleanup settings.
// Cleanup is automatically enabled when OlderThanDays or KeepLast is set (> 0).
type CloudflareCleanupConfig struct {
	OlderThanDays int
	KeepLast      int
}

// New creates a new Cloudflare Pages provider.
func NewCloudflare(apiToken, accountID, projectName string, cleanup CloudflareCleanupConfig, repositories []*config.RepositoryConfig, poolMode string) (*PagesProvider, error) {
	return &PagesProvider{
		accountID:     accountID,
		projectName:   projectName,
		apiToken:      apiToken,
		cleanupConfig: cleanup,
		repositories:  repositories,
		poolMode:      poolMode,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Publish uploads files to Cloudflare Pages using Direct Upload API.
func (p *PagesProvider) Publish(ctx context.Context, outputDir string) error {
	slog.Info("Starting Cloudflare Pages deployment", "project", p.projectName)

	// Resolve symlink if outputDir is a symlink
	resolvedDir, err := filepath.EvalSymlinks(outputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output directory: %w", err)
	}
	outputDir = resolvedDir

	// Collect all files
	files, err := p.collectFiles(outputDir)
	if err != nil {
		return fmt.Errorf("failed to collect files: %w", err)
	}
	slog.Info("Collected files for upload", "count", len(files))

	// Get JWT upload token
	jwt, err := p.getUploadToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get upload token: %w", err)
	}

	// Build manifest (path -> hash mapping)
	manifest, fileHashes, err := p.buildManifest(outputDir, files)
	if err != nil {
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	// Check which files are already uploaded (can skip)
	missingHashes, err := p.checkMissingHashes(ctx, jwt, fileHashes)
	if err != nil {
		return fmt.Errorf("failed to check missing hashes: %w", err)
	}
	slog.Info("Upload status", "total", len(fileHashes), "missing", len(missingHashes), "skipped", len(fileHashes)-len(missingHashes))

	// Upload only missing files
	if len(missingHashes) > 0 {
		if err := p.uploadAssets(ctx, jwt, outputDir, files, manifest, missingHashes); err != nil {
			return fmt.Errorf("failed to upload assets: %w", err)
		}
	}

	// Create deployment with manifest and special files
	deploymentID, deploymentURL, err := p.createDeployment(ctx, outputDir, manifest)
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	// Wait for deployment to complete
	slog.Info("Waiting for deployment to complete...")
	if err := p.waitForDeployment(ctx, deploymentID); err != nil {
		return fmt.Errorf("deployment failed: %w", err)
	}

	slog.Info("Successfully deployed to Cloudflare Pages",
		"project", p.projectName,
		"preview_url", deploymentURL,
		"production_url", p.GetURL())

	// Cleanup old deployments if criteria are configured
	if p.cleanupConfig.OlderThanDays > 0 || p.cleanupConfig.KeepLast > 0 {
		if err := p.cleanupOldDeployments(ctx); err != nil {
			slog.Warn("Failed to cleanup old deployments", "error", err)
			// Don't fail the deployment if cleanup fails
		}
	}

	return nil
}

// GetURL returns the production URL for the project.
func (p *PagesProvider) GetURL() string {
	return fmt.Sprintf("https://%s.pages.dev", p.projectName)
}

// collectFiles scans the directory and returns list of files to upload.
// Excludes _redirects and _headers as they need special handling.
func (p *PagesProvider) collectFiles(outputDir string) ([]string, error) {
	var files []string

	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			return err
		}

		// Convert to forward slashes for web paths
		relPath = filepath.ToSlash(relPath)

		// Skip _redirects and _headers - they need special handling in deployment
		if relPath == "_redirects" || relPath == "_headers" {
			return nil
		}

		files = append(files, relPath)
		return nil
	})

	return files, err
}

// getUploadToken fetches a JWT token for uploading assets
func (p *PagesProvider) getUploadToken(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects/%s/upload-token",
		p.accountID, p.projectName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+p.apiToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get upload token (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool `json:"success"`
		Result  struct {
			JWT string `json:"jwt"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if !result.Success || result.Result.JWT == "" {
		return "", fmt.Errorf("no JWT token in response")
	}

	slog.Debug("Obtained upload token")
	return result.Result.JWT, nil
}

// buildManifest creates a map of file paths to their BLAKE3 hashes.
func (p *PagesProvider) buildManifest(outputDir string, files []string) (map[string]string, []string, error) {
	manifest := make(map[string]string)
	var hashes []string

	for _, relPath := range files {
		fullPath := filepath.Join(outputDir, relPath)
		hash, err := computeFileHash(fullPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to hash %s: %w", relPath, err)
		}

		// Cloudflare expects paths WITH leading slash in manifest
		manifestPath := "/" + strings.TrimPrefix(relPath, "/")
		manifest[manifestPath] = hash
		hashes = append(hashes, hash)
	}

	return manifest, hashes, nil
}

// checkMissingHashes checks which file hashes need to be uploaded.
func (p *PagesProvider) checkMissingHashes(ctx context.Context, jwt string, hashes []string) ([]string, error) {
	url := "https://api.cloudflare.com/client/v4/pages/assets/check-missing"

	payload := map[string][]string{
		"hashes": hashes,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to check missing hashes (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool     `json:"success"`
		Result  []string `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Result, nil
}

// uploadFile represents a file to upload to Pages.
type uploadFile struct {
	Key      string            `json:"key"`      // File hash
	Value    string            `json:"value"`    // Base64-encoded file content
	Metadata map[string]string `json:"metadata"` // File metadata (contentType, etc)
	Base64   bool              `json:"base64"`   // Always true for Pages
}

// uploadAssets uploads the actual file contents as base64-encoded JSON.
func (p *PagesProvider) uploadAssets(ctx context.Context, jwt string, outputDir string, files []string, manifest map[string]string, missingHashes []string) error {
	// Create a set of missing hashes for quick lookup
	missingSet := make(map[string]bool)
	for _, hash := range missingHashes {
		missingSet[hash] = true
	}

	// Build upload payload
	var uploads []uploadFile

	for _, relPath := range files {
		manifestPath := "/" + strings.TrimPrefix(relPath, "/")
		hash := manifest[manifestPath]

		// Skip if not in missing list
		if !missingSet[hash] {
			continue
		}

		fullPath := filepath.Join(outputDir, relPath)

		// Read file
		fileData, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", relPath, err)
		}

		// Encode to base64
		encoded := base64.StdEncoding.EncodeToString(fileData)

		// Get content type
		contentType := mime.TypeByExtension(filepath.Ext(relPath))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		uploads = append(uploads, uploadFile{
			Key:   hash,
			Value: encoded,
			Metadata: map[string]string{
				"contentType": contentType,
			},
			Base64: true,
		})

		slog.Debug("Preparing file for upload", "path", relPath, "hash", hash[:8])
	}

	if len(uploads) == 0 {
		slog.Info("No new files to upload")
		return nil
	}

	// Upload in batches (Cloudflare has size limits)
	const maxBatchSize = 50 * 1024 * 1024 // 50 MB

	for i := 0; i < len(uploads); i++ {
		batch := []uploadFile{uploads[i]}

		// Add more files to batch if they fit
		batchSize := len(uploads[i].Value)
		for j := i + 1; j < len(uploads) && batchSize < maxBatchSize; j++ {
			if batchSize+len(uploads[j].Value) > maxBatchSize {
				break
			}
			batch = append(batch, uploads[j])
			batchSize += len(uploads[j].Value)
			i = j
		}

		if err := p.uploadBatch(ctx, jwt, batch); err != nil {
			return err
		}
	}

	slog.Info("Uploaded files", "count", len(uploads))
	return nil
}

// uploadBatch uploads a batch of files.
func (p *PagesProvider) uploadBatch(ctx context.Context, jwt string, batch []uploadFile) error {
	url := "https://api.cloudflare.com/client/v4/pages/assets/upload?base64=true"

	jsonData, err := json.Marshal(batch)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to upload batch (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool `json:"success"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("upload batch failed: %s", string(body))
	}

	return nil
}

// createDeployment creates the deployment with the manifest.
func (p *PagesProvider) createDeployment(ctx context.Context, outputDir string, manifest map[string]string) (string, string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects/%s/deployments",
		p.accountID, p.projectName)

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return "", "", err
	}

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add manifest field
	if err := writer.WriteField("manifest", string(manifestJSON)); err != nil {
		return "", "", err
	}

	// Add branch field
	if err := writer.WriteField("branch", "main"); err != nil {
		return "", "", err
	}

	// Add _headers file if it exists
	headersPath := filepath.Join(outputDir, "_headers")
	if headersData, err := os.ReadFile(headersPath); err == nil {
		part, err := writer.CreateFormFile("_headers", "_headers")
		if err != nil {
			return "", "", fmt.Errorf("failed to create _headers form field: %w", err)
		}
		if _, err := part.Write(headersData); err != nil {
			return "", "", fmt.Errorf("failed to write _headers data: %w", err)
		}
		slog.Debug("Including _headers file in deployment")
	}

	// Generate and add _redirects file if in redirect mode
	if p.poolMode == "redirect" {
		redirectsData, err := p.generateRedirects()
		if err != nil {
			return "", "", fmt.Errorf("failed to generate redirects: %w", err)
		}
		if redirectsData != nil {
			part, err := writer.CreateFormFile("_redirects", "_redirects")
			if err != nil {
				return "", "", fmt.Errorf("failed to create _redirects form field: %w", err)
			}
			if _, err := part.Write(redirectsData); err != nil {
				return "", "", fmt.Errorf("failed to write _redirects data: %w", err)
			}
			slog.Debug("Generated and included _redirects file for Cloudflare Pages", "content", string(redirectsData))
		}
	}

	if err := writer.Close(); err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("deployment failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool `json:"success"`
		Result  struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}

	if !result.Success || result.Result.ID == "" {
		return "", "", fmt.Errorf("no deployment ID in response: %s", string(body))
	}

	slog.Info("Deployment created", "id", result.Result.ID)
	return result.Result.ID, result.Result.URL, nil
}

// waitForDeployment polls the deployment status until it's complete.
func (p *PagesProvider) waitForDeployment(ctx context.Context, deploymentID string) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects/%s/deployments/%s",
		p.accountID, p.projectName, deploymentID)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("deployment timed out after 5 minutes")
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return err
			}

			req.Header.Set("Authorization", "Bearer "+p.apiToken)

			resp, err := p.httpClient.Do(req)
			if err != nil {
				return err
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}

			var result struct {
				Success bool `json:"success"`
				Result  struct {
					LatestStage struct {
						Name   string `json:"name"`
						Status string `json:"status"`
					} `json:"latest_stage"`
				} `json:"result"`
			}

			if err := json.Unmarshal(body, &result); err != nil {
				return err
			}

			stageName := result.Result.LatestStage.Name
			stageStatus := result.Result.LatestStage.Status

			slog.Debug("Deployment stage update", "stage", stageName, "status", stageStatus)

			// Check if deployment is complete
			if stageName == "deploy" && stageStatus == "success" {
				return nil
			}

			// Check for failures
			if stageStatus == "failure" || stageStatus == "canceled" {
				return fmt.Errorf("deployment failed at stage %s with status %s", stageName, stageStatus)
			}
		}
	}
}

// computeFileHash calculates BLAKE3 hash of a file matching wrangler's algorithm.
// Hash input: base64(fileContent) + fileExtension
// Returns: first 32 hex characters (16 bytes)
func computeFileHash(path string) (string, error) {
	// Read file content
	fileData, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	// Encode to base64 (like wrangler does)
	encoded := base64.StdEncoding.EncodeToString(fileData)

	// Get file extension (without the dot)
	ext := filepath.Ext(path)
	if len(ext) > 0 {
		ext = ext[1:] // Remove leading dot
	}

	// Hash: base64content + extension
	hasher := blake3.New()
	_, _ = hasher.Write([]byte(encoded + ext))
	hash := hasher.Sum(nil)

	// Return first 32 hex characters (16 bytes)
	return hex.EncodeToString(hash[:16]), nil
}

// deploymentInfo contains information about a Pages deployment.
type deploymentInfo struct {
	ID          string    `json:"id"`
	CreatedOn   time.Time `json:"created_on"`
	Environment string    `json:"environment"`
}

// listDeployments fetches all deployments for the project.
func (p *PagesProvider) listDeployments(ctx context.Context) ([]deploymentInfo, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects/%s/deployments",
		p.accountID, p.projectName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+p.apiToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list deployments: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Result []deploymentInfo `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.Result, nil
}

// deleteDeployment deletes a specific deployment by ID.
func (p *PagesProvider) deleteDeployment(ctx context.Context, deploymentID string) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects/%s/deployments/%s",
		p.accountID, p.projectName, deploymentID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.apiToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete deployment: %s - %s", resp.Status, string(body))
	}

	return nil
}

// cleanupOldDeployments removes deployments based on cleanup configuration.
func (p *PagesProvider) cleanupOldDeployments(ctx context.Context) error {
	slog.Info("Starting deployment cleanup", "older_than_days", p.cleanupConfig.OlderThanDays, "keep_last", p.cleanupConfig.KeepLast)

	// Fetch all deployments
	deployments, err := p.listDeployments(ctx)
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(deployments) == 0 {
		slog.Info("No deployments to clean up")
		return nil
	}

	var toDelete []deploymentInfo
	now := time.Now()

	// Filter deployments to delete based on cleanup rules
	for _, dep := range deployments {
		// Check age-based cleanup
		if p.cleanupConfig.OlderThanDays > 0 {
			age := now.Sub(dep.CreatedOn)
			if age > time.Duration(p.cleanupConfig.OlderThanDays)*24*time.Hour {
				toDelete = append(toDelete, dep)
			}
		}
	}

	// Apply count-based limit (keep last N deployments)
	if p.cleanupConfig.KeepLast > 0 && len(deployments) > p.cleanupConfig.KeepLast {
		// Sort deployments by creation time (newest first)
		sortedDeps := make([]deploymentInfo, len(deployments))
		copy(sortedDeps, deployments)

		// Sort by CreatedOn descending
		for i := range sortedDeps {
			for j := i + 1; j < len(sortedDeps); j++ {
				if sortedDeps[j].CreatedOn.After(sortedDeps[i].CreatedOn) {
					sortedDeps[i], sortedDeps[j] = sortedDeps[j], sortedDeps[i]
				}
			}
		}

		// Mark older deployments for deletion (keeping the newest KeepLast)
		for i := p.cleanupConfig.KeepLast; i < len(sortedDeps); i++ {
			dep := sortedDeps[i]

			// Check if already in toDelete list
			found := false
			for _, existing := range toDelete {
				if existing.ID == dep.ID {
					found = true
					break
				}
			}
			if !found {
				toDelete = append(toDelete, dep)
			}
		}
	}

	if len(toDelete) == 0 {
		slog.Info("No deployments match cleanup criteria")
		return nil
	}

	// Delete deployments
	deleted := 0
	failed := 0
	for _, dep := range toDelete {
		slog.Debug("Deleting deployment", "id", dep.ID, "created", dep.CreatedOn.Format("2006-01-02"))

		if err := p.deleteDeployment(ctx, dep.ID); err != nil {
			slog.Warn("Failed to delete deployment", "id", dep.ID, "error", err)
			failed++
		} else {
			deleted++
		}
	}

	slog.Info("Cleanup complete", "deleted", deleted, "failed", failed)

	if failed > 0 {
		return fmt.Errorf("%d deployments failed to delete", failed)
	}

	return nil
}

// generateRedirects creates the _redirects file content for Cloudflare Pages.
// Returns nil if no feeds requiring redirects are found.
func (p *PagesProvider) generateRedirects() ([]byte, error) {
	var buf bytes.Buffer
	hasRedirects := false

	// Trust patterns: detect services that support user-scoped redirects
	// This keeps redirect count low while maintaining trust boundaries
	// TODO: make patterns configurable so more domains can be added in future

	// 1. GitHub: pool/github.com/owner/repo/releases/download/...
	// Per-owner redirects for trustworthiness
	githubOwners := make(map[string]bool)
	for _, repo := range p.repositories {
		for _, feedOpts := range repo.Feeds {
			if feed.FeedType(feedOpts.Type) == feed.FeedTypeGitHub {
				parts := strings.Split(feedOpts.Name, "/")
				if len(parts) == 2 {
					githubOwners[parts[0]] = true
				}
			}
		}
	}

	if len(githubOwners) > 0 {
		hasRedirects = true

		// GitHub .dsc files redirect to dsc/ subdirectory
		// .dsc files in dsc/ contain corrected filenames (GitHub normalizes ~ to .)
		fmt.Fprintf(&buf, "/:aptrepo/pool/github.com/*.dsc /:aptrepo/dsc/github.com/:splat.dsc 301\n")

		// Per-owner redirects for all other files
		for owner := range githubOwners {
			fmt.Fprintf(&buf, "/:aptrepo/pool/github.com/%s/:repo/* https://github.com/%s/:repo/releases/download/:splat 301\n", owner, owner)
		}
	}

	// 2. OBS personal repositories (download.opensuse.org with home: prefix)
	// Per-user redirects for trustworthiness
	// Example: pool/download.opensuse.org/repositories/home:/dionysius:/immich/Debian_13/...
	// -> https://download.opensuse.org/repositories/home:/dionysius:/:splat
	// Note: Colons NOT followed by letters are treated as literals (no escaping needed)
	obsUsers := make(map[string]bool)
	for _, repo := range p.repositories {
		for _, feedOpts := range repo.Feeds {
			if feed.FeedType(feedOpts.Type) == feed.FeedTypeOBS {
				// Check if it's download.opensuse.org
				if feedOpts.DownloadURL != nil && feedOpts.DownloadURL.Host == "download.opensuse.org" {
					// Extract user from Name (e.g., "home:dionysius:immich")
					if strings.HasPrefix(feedOpts.Name, "home:") {
						parts := strings.Split(feedOpts.Name, ":")
						if len(parts) >= 2 {
							user := parts[1]
							obsUsers[user] = true
						}
					}
				}
			}
		}
	}

	for user := range obsUsers {
		// Colons followed by / or at end are literals, no escaping needed
		fmt.Fprintf(&buf, "/:aptrepo/pool/download.opensuse.org/repositories/home:/%s:/* https://download.opensuse.org/repositories/home:/%s:/:splat 301\n", user, user)
		hasRedirects = true
	}

	// 3. APT and other feeds (per-domain redirects to minimize redirect count)
	// Collect unique domains from DownloadURL
	// Example: pool/deb.debian.org/... -> https://deb.debian.org/:splat
	// This creates ONE redirect per domain, regardless of how many repos from that domain
	domains := make(map[string]bool)

	for _, repo := range p.repositories {
		for _, feedOpts := range repo.Feeds {
			if feedOpts.DownloadURL == nil {
				continue
			}

			domain := feedOpts.DownloadURL.Host

			// Skip if already handled by trust patterns above
			if domain == "github.com" {
				continue // GitHub handled by per-owner pattern
			}
			if domain == "download.opensuse.org" &&
				feed.FeedType(feedOpts.Type) == feed.FeedTypeOBS &&
				strings.HasPrefix(feedOpts.Name, "home:") {
				continue // OBS personal repos handled by per-user pattern
			}

			domains[domain] = true
		}
	}

	for domain := range domains {
		// One redirect per domain - matches any path under that domain
		fmt.Fprintf(&buf, "/:aptrepo/pool/%s/* https://%s/:splat 301\n", domain, domain)
		hasRedirects = true
	}

	if !hasRedirects {
		return nil, nil
	}

	return buf.Bytes(), nil
}
