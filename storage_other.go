//go:build !linux

package main

import "errors"

type storageCapacity struct {
	Free  uint64
	Total uint64
}

func getStorageCapacity(path string) (storageCapacity, error) {
	return storageCapacity{}, errors.New("capacity reporting is not implemented for this operating system")
}
