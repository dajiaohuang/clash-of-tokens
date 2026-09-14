//go:build !windows

package browserpassword

import (
	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/credentials"
	"context"
)

func countProfile(context.Context, browsermeta.Candidate) (int, error) { return 0, ErrUnsupported }
func readProfile(context.Context, browsermeta.Candidate) ([]credentials.ImportEntry, error) {
	return nil, ErrUnsupported
}
