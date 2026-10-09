package git

import (
	"context"
	"errors"
	"os/exec"
)

// IsAncestor checks history without changing refs or relying on mutable branch names.
func IsAncestor(ctx context.Context, root, base, head string) (bool, error) {
	a, e := Resolve(ctx, root, base)
	if e != nil {
		return false, e
	}
	b, e := Resolve(ctx, root, head)
	if e != nil {
		return false, e
	}
	_, e = run(ctx, root, "merge-base", "--is-ancestor", a, b)
	if e == nil {
		return true, nil
	}
	var ex *exec.ExitError
	if errors.As(e, &ex) && ex.ExitCode() == 1 {
		return false, nil
	}
	return false, e
}
