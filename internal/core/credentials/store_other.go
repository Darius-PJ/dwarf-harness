//go:build !windows || !amd64

package credentials

func (nativeSource) Bind() (*Secret, error) { return nil, ErrUnavailable }
