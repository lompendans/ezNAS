//go:build linux

package main

import "syscall"

type storageCapacity struct {
	Free  uint64
	Total uint64
}

func getStorageCapacity(path string) (storageCapacity, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return storageCapacity{}, err
	}
	return storageCapacity{
		Free:  s.Bavail * uint64(s.Bsize),
		Total: s.Blocks * uint64(s.Bsize),
	}, nil
}
