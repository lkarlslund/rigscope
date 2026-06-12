//go:build linux

package collectors

import "syscall"

func filesystemUsage(mount string) (float64, float64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mount, &stat); err != nil {
		return 0, 0, false
	}
	total := float64(stat.Blocks) * float64(stat.Bsize)
	free := float64(stat.Bavail) * float64(stat.Bsize)
	return total, free, true
}
