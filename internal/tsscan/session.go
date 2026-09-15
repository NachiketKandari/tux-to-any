package tsscan

import (
	"context"
	_ "embed"

	"github.com/zema1/wasitter"
)

// wasitterCWasm is the upstream tree-sitter runtime plus the tree-sitter-c
// v0.24.2 grammar, compiled to WASM (wasitter v0.1.0 release asset, sha256
// 5044f382aa9d3b8c200947272c62e9eb5d0678e56089171c54a2e821005286b2). It runs
// under the pure-Go wazero runtime, so the toolchain needs no cgo and no C
// compiler on any platform.
//
//go:embed grammars/wasitter-c.wasm
var wasitterCWasm []byte

// scanSession owns one wasitter runtime + parser pair. Each scan call gets a
// fresh session: a wasitter runtime serializes guest execution, so sharing
// one across parallel workers would turn per-file scans into a convoy.
type scanSession struct {
	parser  *wasitter.Parser
	runtime *wasitter.Runtime
}

func newScanSession() (*scanSession, error) {
	parser, runtime, err := wasitter.NewParserFromWASM(context.Background(), wasitterCWasm)
	if err != nil {
		return nil, err
	}
	return &scanSession{parser: parser, runtime: runtime}, nil
}

// close releases the tree (if any), then the parser, then the runtime.
func (s *scanSession) close(tree *wasitter.Tree) {
	if tree != nil {
		tree.Close()
	}
	s.parser.Close()
	s.runtime.Close()
}
