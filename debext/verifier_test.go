package debext

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/aptly-dev/aptly/pgp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noopVerifier is a test verifier that skips signature verification
type noopVerifier struct{}

func (v *noopVerifier) IsClearSigned(r io.Reader) (bool, error)          { return false, nil }
func (v *noopVerifier) ExtractClearsigned(r io.Reader) (*os.File, error) { return nil, nil }
func (v *noopVerifier) VerifyDetachedSignature(signature, text io.Reader, showKeyInfo bool) error {
	return nil
}
func (v *noopVerifier) VerifyClearsigned(clearsigned io.Reader, showKeyTip bool) (*pgp.KeyInfo, error) {
	return nil, nil
}
func (v *noopVerifier) InitKeyring(bool) error    { return nil }
func (v *noopVerifier) AddKeyring(keyring string) {}

// testVerifier returns a Verifier configured for tests (accepts unsigned, ignores signatures)
func testVerifier() *Verifier {
	return &Verifier{
		Verifier:         &noopVerifier{},
		AcceptUnsigned:   true,
		IgnoreSignatures: true,
	}
}

func TestVerifierConfiguration(t *testing.T) {
	t.Run("AcceptUnsigned", func(t *testing.T) {
		v := &Verifier{
			Verifier:       &noopVerifier{},
			AcceptUnsigned: true,
		}
		content := []byte("unsigned content")
		reader := bytes.NewReader(content)

		rc, keys, err := v.VerifyAndClear(reader)
		assert.NoError(t, err)
		assert.NotNil(t, rc)
		assert.Nil(t, keys)
		err = rc.Close()
		require.NoError(t, err)
	})

	t.Run("RejectUnsigned", func(t *testing.T) {
		v := &Verifier{
			Verifier:       &noopVerifier{},
			AcceptUnsigned: false,
		}
		content := []byte("unsigned content")
		reader := bytes.NewReader(content)

		_, _, err := v.VerifyAndClear(reader)
		assert.ErrorIs(t, err, ErrMissingSignature)
	})

	t.Run("IgnoreSignatures", func(t *testing.T) {
		v := &Verifier{
			Verifier:         &noopVerifier{},
			AcceptUnsigned:   true,
			IgnoreSignatures: true,
		}
		content := []byte("content")
		reader := bytes.NewReader(content)

		rc, keys, err := v.VerifyAndClear(reader)
		assert.NoError(t, err)
		assert.NotNil(t, rc)
		assert.Nil(t, keys) // No verification, so no keys
		_ = rc.Close()
	})
}
