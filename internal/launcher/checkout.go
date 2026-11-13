package launcher

import "errors"

// GitCheckouts makes a restricted ship's checkout as a shared clone of the request's directory.
type GitCheckouts struct{}

func (GitCheckouts) Make(home, session, dir string) (string, error) {
	return "", errors.New("not implemented")
}
