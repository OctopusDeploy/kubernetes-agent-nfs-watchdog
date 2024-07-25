package main

import (
	"os"
	"syscall"
	"testing"
)

func TestIsCorruptedMount(t *testing.T) {
	errorToFail := os.PathError{Err: syscall.ESTALE}
	if !IsCorruptedMnt(&errorToFail) {
		t.Errorf("Expected ESTALE to be caught as a corrupted mount, it wasn't!")
	}
}
