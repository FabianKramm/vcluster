//go:build !linux
// +build !linux

package node

func CheckCgroups() (kubeletRoot, runtimeRoot string, controllers map[string]bool) {
	panic("unimplemented")
}

func EvacuateCgroup2() error {
	panic("unimplemented")
}
