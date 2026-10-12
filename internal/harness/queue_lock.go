package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// queueLock coordinates cleanup, recovery and claims across processes and
// worktrees of this repository. A busy lock defers work to the next loop pass.
func (l *Loop) queueLock(ctx context.Context) (func(), error) {
	dir, err := l.git(ctx, l.Repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "remux-queue.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
