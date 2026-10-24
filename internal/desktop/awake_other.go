//go:build !windows

package desktop

// KeepAwake : hors Windows, D3X garde son terminal et ne retient pas la veille.
func KeepAwake(on bool) bool { return false }
