package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/alitto/pond/v2"
	"github.com/aptly-dev/aptly/pgp"
	"github.com/dionysius/aarg/debext"
	"github.com/dionysius/aarg/internal/common"
	"github.com/dionysius/aarg/internal/config"
	"github.com/google/go-github/v80/github"
)

// Application holds the initialized runtime components and configuration
type Application struct {
	Config             *config.Config
	MainPool           pond.Pool
	DownloadPool       pond.ResultPool[common.Result]
	CompressionPool    pond.ResultPool[common.Result]
	Downloader         *common.Downloader
	DeCompressor       *common.DeCompressor
	Storage            *common.Storage
	GitHubClient       *github.Client
	HTTPClient         *http.Client
	Signer             pgp.Signer
	PublicKeyASCII     []byte // ASCII-armored public key
	PublicKeyBinary    []byte // Binary (dearmored) public key
	PreparedPublicKey  string // Path to prepared public key file
	PreparedPrivateKey string // Path to prepared private key file
	KeyCleanup         func() // Cleanup function for temporary key files
}

// New creates and initializes a new Application from configuration
func New(ctx context.Context, cfg *config.Config) (*Application, error) {
	dirs := cfg.Directories

	// Create worker pools with context (sizes already validated and defaulted in config)
	mainPool := pond.NewPool(int(cfg.Workers.Main), pond.WithContext(ctx), pond.WithoutPanicRecovery())
	downloadPool := pond.NewResultPool[common.Result](int(cfg.Workers.Download), pond.WithContext(ctx), pond.WithoutPanicRecovery())
	compressionPool := pond.NewResultPool[common.Result](int(cfg.Workers.Compression), pond.WithContext(ctx), pond.WithoutPanicRecovery())

	// Initialize HTTP client with optional configuration
	httpClient := &http.Client{}

	// Build base transport
	var transport http.RoundTripper = &http.Transport{}

	// Configure transport if any HTTP options are set
	if cfg.HTTP.MaxIdleConns > 0 || cfg.HTTP.MaxConnsPerHost > 0 {
		baseTransport := &http.Transport{}

		if cfg.HTTP.MaxIdleConns > 0 {
			baseTransport.MaxIdleConns = cfg.HTTP.MaxIdleConns
			baseTransport.MaxIdleConnsPerHost = cfg.HTTP.MaxIdleConns / 10 // Reasonable default
		}
		if cfg.HTTP.MaxConnsPerHost > 0 {
			baseTransport.MaxConnsPerHost = cfg.HTTP.MaxConnsPerHost
		}

		transport = baseTransport
	}

	// Wrap transport with User-Agent setter if configured
	if cfg.HTTP.UserAgent != "" {
		transport = &userAgentTransport{
			Base:      transport,
			UserAgent: cfg.HTTP.UserAgent,
		}
	}

	httpClient.Transport = transport

	// Set timeout
	if cfg.HTTP.Timeout > 0 {
		httpClient.Timeout = time.Duration(cfg.HTTP.Timeout) * time.Second
	}

	// Initialize decompressor with compression pool
	decompressor := common.NewDeCompressor(compressionPool)

	// Initialize downloader with download pool
	downloader := common.NewDownloader(downloadPool, httpClient, decompressor)

	// Initialize storage (using resolved absolute paths from config)
	storage := common.NewStorage(downloader, dirs.GetDownloadsPath(), dirs.GetTrustedPath())

	// Initialize GitHub client (if token is configured)
	var githubClient *github.Client
	if cfg.GitHub.Token != "" {
		githubClient = github.NewClient(httpClient).WithAuthToken(cfg.GitHub.Token)
	} else {
		githubClient = github.NewClient(httpClient)
	}

	// Initialize signer and load public keys
	signer, publicKeyASCII, publicKeyBinary, preparedPublic, preparedPrivate, cleanup, err := initializeSigner(cfg)
	if err != nil {
		return nil, err
	}

	return &Application{
		Config:             cfg,
		MainPool:           mainPool,
		DownloadPool:       downloadPool,
		CompressionPool:    compressionPool,
		Downloader:         downloader,
		DeCompressor:       decompressor,
		Storage:            storage,
		GitHubClient:       githubClient,
		HTTPClient:         httpClient,
		Signer:             signer,
		PublicKeyASCII:     publicKeyASCII,
		PublicKeyBinary:    publicKeyBinary,
		PreparedPublicKey:  preparedPublic,
		PreparedPrivateKey: preparedPrivate,
		KeyCleanup:         cleanup,
	}, nil
}

// Shutdown gracefully stops all application components
func (a *Application) Shutdown() {
	if a.MainPool != nil {
		a.MainPool.StopAndWait()
	}
	if a.DownloadPool != nil {
		a.DownloadPool.StopAndWait()
	}
	if a.CompressionPool != nil {
		a.CompressionPool.StopAndWait()
	}
}

// initializeVerifier creates a verifier for a repository configuration
func (a *Application) initializeVerifier(repoCfg *config.RepositoryConfig) (*debext.Verifier, error) {
	verifier := &pgp.GoVerifier{}

	// Process keyring if specified (keyrings are typically already binary)
	keyringPath := repoCfg.Verification.GetKeyringPath(a.Config.ConfigDir)
	if keyringPath != "" {
		verifier.AddKeyring(keyringPath)
	}

	// Process individual key files (may be ASCII-armored .asc files)
	for _, keyPath := range repoCfg.Verification.GetKeyPaths(a.Config.ConfigDir) {
		keyFile, cleanup, err := prepareKeyFile(keyPath)
		if err != nil {
			return nil, err
		}
		defer cleanup()

		verifier.AddKeyring(keyFile)
	}

	// Initialize the keyring (loads keys into memory)
	if err := verifier.InitKeyring(false); err != nil {
		return nil, err
	}

	// Wrap in our Verifier
	return &debext.Verifier{
		Verifier:         verifier,
		AcceptUnsigned:   false, // Reject unsigned files - all files must be signed
		IgnoreSignatures: false, // Verify all signatures
	}, nil
}

// initializeSigner creates a signer from config and returns public keys in both formats
// TODO: extend debext with better signer handling.
// - offer SetKey() if provided whether or not keyring is set
func initializeSigner(cfg *config.Config) (pgp.Signer, []byte, []byte, string, string, func(), error) {
	signer := &pgp.GoSigner{}

	// Check if custom keys are configured
	privateKeyPath := cfg.Signing.GetPrivateKeyPath(cfg.ConfigDir)
	publicKeyPath := cfg.Signing.GetPublicKeyPath(cfg.ConfigDir)

	var publicKeyASCII, publicKeyBinary []byte
	var preparedPublic, preparedPrivate string
	var cleanupFuncs []func()

	// Combined cleanup function
	cleanup := func() {
		for _, fn := range cleanupFuncs {
			fn()
		}
	}

	// If keys are provided, use them; otherwise GoSigner falls back to pubring.gpg/secring.gpg
	if privateKeyPath != "" && publicKeyPath != "" {
		// Read and store public key
		var err error
		publicKeyASCII, err = os.ReadFile(publicKeyPath)
		if err != nil {
			return nil, nil, nil, "", "", nil, err
		}

		// Convert ASCII key to binary format
		publicKeyBinary, err = armorDecode(publicKeyASCII)
		if err != nil {
			return nil, nil, nil, "", "", nil, err
		}

		// Prepare key files (convert to binary if needed)
		preparedPublic, cleanupPublic, err := prepareKeyFile(publicKeyPath)
		if err != nil {
			return nil, nil, nil, "", "", nil, err
		}
		cleanupFuncs = append(cleanupFuncs, cleanupPublic)

		preparedPrivate, cleanupPrivate, err := prepareKeyFile(privateKeyPath)
		if err != nil {
			cleanup()
			return nil, nil, nil, "", "", nil, err
		}
		cleanupFuncs = append(cleanupFuncs, cleanupPrivate)

		// Set custom keyring paths
		signer.SetKeyRing(preparedPublic, preparedPrivate)
	}

	// Set passphrase if provided
	if cfg.Signing.Passphrase != "" {
		signer.SetPassphrase(cfg.Signing.Passphrase, "")
	}

	// Initialize the signer (loads keys from keyring files into memory)
	if err := signer.Init(); err != nil {
		cleanup()
		return nil, nil, nil, "", "", nil, err
	}

	return signer, publicKeyASCII, publicKeyBinary, preparedPublic, preparedPrivate, cleanup, nil
}

// prepareKeyFile ensures a key file is in binary format for aptly's GoVerifier.
// If the file is ASCII-armored, it converts it to binary in a temp directory.
// Returns the path to use and an optional cleanup function.
func prepareKeyFile(keyPath string) (string, func(), error) {
	// Read the file to detect format
	f, err := os.Open(keyPath)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	// Check if it's ASCII-armored by reading the first 5 bytes
	header := make([]byte, 5)
	n, _ := f.Read(header)
	isArmored := n == 5 && bytes.Equal(header, []byte("-----"))

	if !isArmored {
		// Probably binary format, use as-is (no cleanup needed)
		return keyPath, func() {}, nil
	}

	// ASCII-armored, need to convert to binary
	_, _ = f.Seek(0, 0)

	keys, err := openpgp.ReadArmoredKeyRing(f)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read armored keyring: %w", err)
	}

	// Create temp file for binary keyring
	tmpFile, err := os.CreateTemp("", "aarg-keyring-*.gpg")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp keyring: %w", err)
	}

	// Serialize keys to binary format
	// Check if this is a private keyring by looking for private keys
	hasPrivateKey := false
	for _, entity := range keys {
		if entity.PrivateKey != nil {
			hasPrivateKey = true
			break
		}
	}

	for _, entity := range keys {
		var serializeErr error
		if hasPrivateKey && entity.PrivateKey != nil {
			// Serialize private key
			serializeErr = entity.SerializePrivate(tmpFile, nil)
		} else {
			// Serialize public key
			serializeErr = entity.Serialize(tmpFile)
		}

		if serializeErr != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpFile.Name())
			return "", nil, fmt.Errorf("failed to serialize key: %w", serializeErr)
		}
	}

	// Close the file so data is flushed to disk
	tmpFileName := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFileName)
		return "", nil, fmt.Errorf("failed to close temp keyring: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(tmpFileName)
	}

	return tmpFileName, cleanup, nil
}

// userAgentTransport wraps an http.RoundTripper to set a custom User-Agent header
type userAgentTransport struct {
	Base      http.RoundTripper
	UserAgent string
}

// RoundTrip implements http.RoundTripper
func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Set User-Agent header if not already set
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.UserAgent)
	}

	return t.Base.RoundTrip(req)
}

// armorDecode decodes ASCII-armored data to binary format
func armorDecode(armoredData []byte) ([]byte, error) {
	block, err := armor.Decode(bytes.NewReader(armoredData))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(block.Body)
}
