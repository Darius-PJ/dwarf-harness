//go:build !windows

package winexec

import "context"

// Executor has no native implementation on this platform.
type Executor struct{}

// Open refuses unsupported platforms without creating filesystem resources.
func Open(context.Context, string) (*Executor, error) { return nil, ErrUnsupported }

// Run refuses unsupported platforms without admitting a request.
func (*Executor) Run(context.Context, Request) (Receipt, error) { return Receipt{}, ErrUnsupported }

// Close reports the absence of a supported native executor.
func (*Executor) Close() error { return ErrUnsupported }
