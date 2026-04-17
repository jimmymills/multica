package gitlab

import (
	"context"

	"github.com/multica-ai/multica/server/pkg/secrets"
)

// NewCipherDecrypter adapts a *secrets.Cipher into the TokenDecrypter function
// shape consumed by the resolver and reconciler. Keeping this wrapper in one
// place means callers don't need to re-implement the same closure whenever
// they wire up a gitlab component.
func NewCipherDecrypter(cipher *secrets.Cipher) TokenDecrypter {
	return func(ctx context.Context, encrypted []byte) (string, error) {
		plain, err := cipher.Decrypt(encrypted)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
}
