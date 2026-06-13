//go:build !linux

package collectors

func filesystemUsage(string) (float64, float64, bool) {
	return 0, 0, false
}
