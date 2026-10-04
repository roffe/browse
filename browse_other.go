//go:build !((linux && !android) || freebsd || netbsd || openbsd || windows || (darwin && !ios))

package browse

func show(mode, Options) ([]string, error) {
	return nil, ErrUnavailable
}
