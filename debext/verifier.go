package debext

import (
	"errors"
	"io"

	"github.com/aptly-dev/aptly/pgp"
)

var (
	// ErrSignatureVerificationFailed indicates a signature verification failure
	ErrSignatureVerificationFailed = errors.New("signature verification failed")
	// ErrMissingSignature indicates a file is not signed
	ErrMissingSignature = errors.New("file is not signed")
)

// Verifier wraps aptly's pgp.Verifier with configuration options
type Verifier struct {
	pgp.Verifier
	AcceptUnsigned   bool // Accept files without signatures
	IgnoreSignatures bool // Skip signature verification
}

// VerifyAndClear verifies and extracts cleartext from a clearsigned file.
// The behavior depends on the verifier configuration:
// - If the input is not signed and AcceptUnsigned is false, it returns an error
// - If IgnoreSignatures is true, it extracts cleartext without verification
// - Otherwise, it verifies the signature before extracting cleartext
func (v *Verifier) VerifyAndClear(file io.ReadSeeker) (io.ReadCloser, []pgp.Key, error) {
	isClearSigned, err := v.IsClearSigned(file)
	if err != nil {
		return nil, nil, err
	}

	_, _ = file.Seek(0, 0)

	// Error if not signed and unsigned files are not accepted
	if !isClearSigned && !v.AcceptUnsigned {
		return nil, nil, ErrMissingSignature
	}

	// If IgnoreSignatures is set, just extract cleartext without verification
	if v.IgnoreSignatures {
		if isClearSigned {
			rc, err := v.ExtractClearsigned(file)
			return rc, nil, err
		}
		// Not clearsigned, return the file itself
		return io.NopCloser(file), nil, nil
	}

	// Verify signature if file is clearsigned
	if isClearSigned {
		keyInfo, err := v.VerifyClearsigned(file, false)
		if err != nil {
			// Return signature verification error
			return nil, nil, ErrSignatureVerificationFailed
		}

		_, _ = file.Seek(0, 0)

		rc, err := v.ExtractClearsigned(file)
		return rc, keyInfo.GoodKeys, err
	}

	// Not clearsigned and AcceptUnsigned is true, return as-is
	return io.NopCloser(file), nil, nil
}
