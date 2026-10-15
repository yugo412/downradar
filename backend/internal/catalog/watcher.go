package catalog

import (
	"context"
	"os"
	"time"
)

func Watch(ctx context.Context, path string, interval time.Duration, reload func()) {
	last := modTime(path)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := modTime(path)
			if current.IsZero() || current.Equal(last) {
				continue
			}
			last = current
			reload()
		}
	}
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
