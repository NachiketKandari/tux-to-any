package main

import (
	"fmt"
	"os"

	"tux-to-any/internal/ir"
)

// extractFlowIR resolves the target (file or directory) through the
// extraction path so fragments and the buffer registry apply identically.
func extractFlowIR(target string) ([]*ir.File, error) {
	fi, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("cannot access target path %s: %w", target, err)
	}
	if fi.IsDir() {
		return ir.ExtractDirOpts(target, ir.Options{})
	}
	f, err := ir.ExtractFileOpts(target, ir.Options{})
	if err != nil {
		return nil, err
	}
	return []*ir.File{f}, nil
}
