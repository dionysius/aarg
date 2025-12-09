package debext

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aptly-dev/aptly/deb"
	"github.com/aptly-dev/aptly/utils"
)

const (
	DebugPackageSuffix  = "-dbgsym"
	DebugPackageSection = "debug"
)

// ParseRelease parses an InRelease file and extracts index metadata
func ParseRelease(inReleaseFile string, verifier *Verifier) (*Release, error) {
	file, err := os.Open(inReleaseFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", inReleaseFile, err)
	}
	defer func() { _ = file.Close() }()

	// Verify GPG signature
	reader, keys, err := verifier.VerifyAndClear(file)
	if err != nil {
		return nil, fmt.Errorf("%s: signature verification failed: %w", inReleaseFile, err)
	}
	defer func() { _ = reader.Close() }()
	if len(keys) > 0 {
		slog.Debug("Signature verified", "file", filepath.Base(inReleaseFile), "with", keys)
	}

	// Read all content for parsing
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to read: %w", inReleaseFile, err)
	}

	// Parse as stanza
	stanzaReader := deb.NewControlFileReader(strings.NewReader(string(content)), false, false)
	stanza, err := stanzaReader.ReadStanza()
	if err != nil {
		return nil, fmt.Errorf("%s: failed to parse stanza: %w", inReleaseFile, err)
	}

	config := &Release{
		Origin:        stanza["Origin"],
		Label:         stanza["Label"],
		Suite:         stanza["Suite"],
		Codename:      stanza["Codename"],
		Architectures: strings.Fields(stanza["Architectures"]),
		Components:    strings.Fields(stanza["Components"]),
		Description:   stanza["Description"],
		Files:         make(map[string]utils.ChecksumInfo),
	}

	// Parse Date - try multiple formats for compatibility
	// RFC 2822/1123 is the spec, but some repositories use other formats
	dateFormats := []string{
		"Mon, 2 Jan 2006 15:04:05 MST",     // RFC 1123 with timezone (spec)
		"Mon, 2 Jan 2006 15:04:05 -0700",   // RFC 1123 with numeric timezone
		"Mon Jan _2 15:04:05 2006",         // Unix date format (no timezone)
		"Mon Jan _2 15:04:05 2006 MST",     // Unix date format with timezone
		time.RFC1123Z,                      // Go stdlib RFC1123 with numeric zone
		time.RFC1123,                       // Go stdlib RFC1123
	}
	
	var parseErr error
	for _, format := range dateFormats {
		config.Date, parseErr = time.Parse(format, stanza["Date"])
		if parseErr == nil {
			// If parsed date has no timezone info, assume UTC
			if config.Date.Location() == time.UTC || config.Date.Location().String() == "UTC" {
				config.Date = config.Date.UTC()
			}
			break
		}
	}
	if parseErr != nil {
		return nil, fmt.Errorf("%s: invalid Date format: %w (tried RFC1123, Unix date formats)", inReleaseFile, parseErr)
	}

	// Parse SHA256 section for index files
	sha256Section := stanza["SHA256"]
	if sha256Section == "" {
		return nil, fmt.Errorf("%s: missing SHA256 section", inReleaseFile)
	}

	// The control file reader concatenates continuation lines with spaces
	// We need to split by space and process groups of 3 fields (hash size filename)
	parts := strings.Fields(sha256Section)
	if len(parts)%3 != 0 {
		return nil, fmt.Errorf("%s: invalid SHA256 section: expected multiple of 3 fields, got %d fields\nSHA256 section: %q",
			inReleaseFile, len(parts), sha256Section)
	}

	for i := 0; i < len(parts); i += 3 {
		hash := parts[i]
		var size int64
		if _, err := fmt.Sscanf(parts[i+1], "%d", &size); err != nil {
			return nil, fmt.Errorf("%s: invalid size in SHA256 entry %d: %w", inReleaseFile, i/3+1, err)
		}
		filename := parts[i+2]

		config.Files[filename] = utils.ChecksumInfo{
			Size:   size,
			SHA256: hash,
		}
	}

	return config, nil
}

// ParsePackageIndex parses a Packages or Sources index file and returns packages
func ParsePackageIndex(path string, isSource bool) ([]*deb.Package, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var packages []*deb.Package
	controlReader := deb.NewControlFileReader(file, false, false)

	for {
		stanza, err := controlReader.ReadStanza()
		if err != nil {
			return nil, fmt.Errorf("%s: failed to read stanza: %w", path, err)
		}
		if stanza == nil {
			// EOF reached (ReadStanza returns nil stanza, nil error at EOF)
			break
		}

		var pkg *deb.Package
		if isSource {
			pkg, err = deb.NewSourcePackageFromControlFile(stanza)
			if err != nil {
				return nil, fmt.Errorf("%s: failed to parse source package: %w", path, err)
			}
		} else {
			pkg = deb.NewPackageFromControlFile(stanza)
		}

		packages = append(packages, pkg)
	}

	return packages, nil
}

// ParseBinary creates a *deb.Package from a .deb file with proper pool path and checksums.
// It parses the control file, calculates checksums, and sets the required fields.
func ParseBinary(debFile string, poolPath string) (*deb.Package, error) {
	// Parse the .deb control file
	stanza, err := deb.GetControlFileFromDeb(debFile)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", debFile, err)
	}
	// Calculate checksums using aptly's utility
	checksums, err := utils.ChecksumsForFile(debFile)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to calculate checksums: %w", debFile, err)
	}

	// Set the Filename field to poolPath/filename
	filename := filepath.Base(debFile)
	filePath := filepath.Join(poolPath, filename)

	// Set the fields in the stanza
	stanza["Filename"] = filePath
	stanza["Size"] = fmt.Sprintf("%d", checksums.Size)
	stanza["MD5sum"] = checksums.MD5
	stanza["SHA1"] = checksums.SHA1
	stanza["SHA256"] = checksums.SHA256
	stanza["SHA512"] = checksums.SHA512

	// Note: Section field is preserved in stanza and accessible via pkg.Extra()["Section"]
	return deb.NewPackageFromControlFile(stanza), nil
}

// ParseSource creates a *deb.Package from a .dsc file with proper directory path and checksums.
// It verifies the signature, parses the control file, and processes all referenced source files.
func ParseSource(dscFile string, verifier *Verifier, poolPath string) (*deb.Package, error) {
	// Parse and verify the .dsc file
	file, err := os.Open(dscFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dscFile, err)
	}
	defer func() { _ = file.Close() }()

	text, keys, err := verifier.VerifyAndClear(file)
	if err != nil {
		return nil, fmt.Errorf("%s: signature verification failed: %w", dscFile, err)
	}
	defer func() { _ = text.Close() }()
	if len(keys) > 0 {
		slog.Debug("Signature verified", "file", filepath.Base(dscFile), "with", keys)
	}

	reader := deb.NewControlFileReader(text, false, false)
	stanza, err := reader.ReadStanza()
	if err != nil {
		return nil, fmt.Errorf("%s: failed to parse stanza: %w", dscFile, err)
	}
	// Rename Source to Package (source packages use Source, but Sources file uses Package)
	if sourceName, ok := stanza["Source"]; ok {
		stanza["Package"] = sourceName
		delete(stanza, "Source")
	}

	// Set Directory field to the pool path
	stanza["Directory"] = poolPath

	// Create source package from the .dsc control file
	src, err := deb.NewSourcePackageFromControlFile(stanza)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to create source package: %w", dscFile, err)
	}

	// Get the files that were parsed from the .dsc
	files := src.Files()

	// Calculate checksums for the .dsc file itself
	dscChecksums, err := utils.ChecksumsForFile(dscFile)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to calculate checksums: %w", dscFile, err)
	}

	// Add the .dsc file to the files list
	dscPackageFile := deb.PackageFile{
		Filename:  filepath.Base(dscFile),
		Checksums: dscChecksums,
	}

	// Update package with all files including the .dsc
	src.UpdateFiles(append(files, dscPackageFile))

	return src, nil
}

// ParseChanges parses a .changes file
func ParseChanges(changesFile string, verifier *Verifier) (*deb.Changes, error) {
	// Create Changes struct directly without temp directory/copying
	// TempDir is set to the actual directory where the file is located
	changes := &deb.Changes{
		BasePath:    filepath.Dir(changesFile),
		ChangesName: filepath.Base(changesFile),
		TempDir:     filepath.Dir(changesFile),
	}

	// Verify and parse the changes file using aptly's VerifyAndParse with our options
	err := changes.VerifyAndParse(verifier.AcceptUnsigned, verifier.IgnoreSignatures, verifier.Verifier)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to verify and parse: %w", changesFile, err)
	}

	if len(changes.SignatureKeys) > 0 {
		slog.Debug("Signature verified", "file", filepath.Base(changesFile), "with", changes.SignatureKeys)
	}

	return changes, nil
}

// GeneratePackageIndex generates a package index file (Packages or Sources) and writes to w.
// Set isSource to true for Sources files, false for Packages files.
func GeneratePackageIndex(w io.Writer, list *deb.PackageList, isSource bool) error {
	// PrepareIndex sorts the packages by name and version (latest to oldest)
	list.PrepareIndex()

	// Write all packages using aptly's canonical formatting
	bufWriter := bufio.NewWriter(w)

	err := list.ForEachIndexed(func(pkg *deb.Package) error {
		// WriteTo handles canonical field ordering internally
		if err := pkg.Stanza().WriteTo(bufWriter, isSource, false, false); err != nil {
			return err
		}
		// Add blank line between stanzas
		if err := bufWriter.WriteByte('\n'); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return err
	}

	return bufWriter.Flush()
}

// Release holds configuration for generating Release file
type Release struct {
	Origin        string
	Label         string
	Suite         string
	Codename      string
	Date          time.Time
	Architectures []string
	Components    []string
	Description   string
	// Files maps relative paths to their checksums (following aptly's indexFiles.generatedFiles pattern)
	Files map[string]utils.ChecksumInfo
}

// GenerateRelease creates a Release file using aptly's Stanza formatting and writes to w.
func GenerateRelease(w io.Writer, config Release) error {
	release := make(deb.Stanza)

	release["Origin"] = config.Origin
	release["Label"] = config.Label
	release["Suite"] = config.Suite
	release["Codename"] = config.Codename
	release["Date"] = config.Date.UTC().Format("Mon, 2 Jan 2006 15:04:05 MST")
	release["Architectures"] = strings.Join(config.Architectures, " ")
	release["Components"] = strings.Join(config.Components, " ")
	// Description is a multiline field, needs leading space and trailing newline (aptly pattern)
	release["Description"] = " " + config.Description + "\n"

	// Build checksum sections (following aptly's pattern in publish.go lines 1143-1150)
	release["MD5Sum"] = ""
	release["SHA1"] = ""
	release["SHA256"] = ""
	release["SHA512"] = ""

	// Sort paths for deterministic output
	sortedPaths := slices.Sorted(maps.Keys(config.Files))

	for _, path := range sortedPaths {
		info := config.Files[path]
		release["MD5Sum"] += fmt.Sprintf(" %s %8d %s\n", info.MD5, info.Size, path)
		release["SHA1"] += fmt.Sprintf(" %s %8d %s\n", info.SHA1, info.Size, path)
		release["SHA256"] += fmt.Sprintf(" %s %8d %s\n", info.SHA256, info.Size, path)
		release["SHA512"] += fmt.Sprintf(" %s %8d %s\n", info.SHA512, info.Size, path)
	}

	// Use aptly's WriteTo for canonical Release file formatting
	bufWriter := bufio.NewWriter(w)

	// isSource=false, isRelease=true, isInstaller=false
	if err := release.WriteTo(bufWriter, false, true, false); err != nil {
		return err
	}

	return bufWriter.Flush()
}

// GetSourceNameFromPackage returns the source package name for a given package.
// If a binary package has the same name as the source, it won't have the source field set.
func GetSourceNameFromPackage(pkg *deb.Package) string {
	// Source packages: use Name
	if pkg.IsSource {
		return pkg.Name
	}

	// Binary packages: use Source if set, otherwise Name
	if pkg.Source != "" {
		return pkg.Source
	}

	return pkg.Name
}

// IsDebugByName determines if a package a debug package by its name.
// Try to use IsDebugPackage instead where possible.
func IsDebugByName(input string) bool {
	return strings.HasSuffix(input, DebugPackageSuffix)
}

// IsDebugPackageByFilename determines if a package is a debug package by its filename.
// Try to use IsDebugPackage instead where possible.
func IsDebugPackageByFilename(filename string) bool {
	return strings.Contains(filename, DebugPackageSuffix+"_") || strings.HasSuffix(filename, ".ddeb")
}

// IsDebugPackage determines if a package is a debug package.
func IsDebugPackage(pkg *deb.Package) bool {
	if pkg.IsSource {
		return false
	}
	if section, ok := pkg.Extra()["Section"]; ok && section == DebugPackageSection {
		return true
	}

	return IsDebugByName(pkg.Name)
}

// GetPoolPath returns the pool directory path for a package.
// Uses Debian convention: pool/component/first-letter/package-name
// Packages starting with "lib" use first 4 characters (lib + one letter).
func GetPoolPath(component, packageName string) string {
	firstLetter := string(packageName[0])
	if strings.HasPrefix(packageName, "lib") && len(packageName) > 3 {
		firstLetter = packageName[:4]
	}
	return filepath.Join("pool", component, firstLetter, packageName)
}

// ModifyPackageStanza modifies a package's stanza field and updates the package pointer in place.
// This is necessary because aptly's Stanza() returns a copy, so direct modifications don't persist.
// The function handles both binary and source packages appropriately.
func ModifyPackageStanza(pkg **deb.Package, key, value string) error {
	// Get a copy of the stanza
	stanza := (*pkg).Stanza()

	// Apply modification
	stanza[key] = value

	// Recreate package with modified stanza
	var newPkg *deb.Package
	var err error

	if (*pkg).IsSource {
		newPkg, err = deb.NewSourcePackageFromControlFile(stanza)
	} else {
		newPkg = deb.NewPackageFromControlFile(stanza)
	}

	if err != nil {
		return err
	}

	// Update the pointer
	*pkg = newPkg
	return nil
}
