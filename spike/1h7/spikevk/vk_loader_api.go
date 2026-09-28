package spikevk

import (
	"os"
	"strconv"
)

// EnvDeviceIndex returns BLOXWM_VK_DEVICE or "".
func EnvDeviceIndex() string { return os.Getenv("BLOXWM_VK_DEVICE") }

// parseInt parses a non-negative decimal integer, or -1.
func parseInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// ResultError wraps a raw VkResult with the failing call's name: the Go
// version of the C code's `return (int)res` mud, but greppable.
type resultError struct {
	res  Result
	call string
}

func ResultError(res Result, call string) error { return &resultError{res, call} }

func (e *resultError) Error() string {
	return e.call + ": " + e.res.Error()
}

// missingSymbolError reports an entry point the loader could not resolve.
type missingSymbolError struct{ name string }

func (e *missingSymbolError) Error() string {
	return "vulkan entry point not resolvable at runtime: " + e.name
}

func errMissing(name string) error { return &missingSymbolError{name} }
