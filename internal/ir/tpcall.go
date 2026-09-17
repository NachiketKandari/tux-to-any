package ir

import (
	"tux-to-any/internal/tsscan"
)

// buildTPCalls correlates every tpcall with its FML contracts: send =
// Fadd32-into-send-buffer ops before the call, recv = Fget32-from-recv-
// buffer ops after it, both inside the innermost enclosing branch block
// (else the function body). Identified buffers with empty contracts on both
// sides are recorded as ambiguous — never guessed away.
func buildTPCalls(facts *tsscan.SourceFacts, f *File, ops []FmlOp, opts Options) []TPCall {
	var out []TPCall
	for i := range facts.Calls {
		c := &facts.Calls[i]
		if !c.IsTpCall {
			continue
		}
		args := splitArgs(c.Args)
		tc := TPCall{
			StartLine: c.Line,
			EndLine:   c.Line,
			Function:  c.Func,
			Async:     c.Name == "tpacall",
		}
		if len(args) > 0 {
			tc.Service = stringLit(args[0])
		}
		if len(args) > 1 {
			tc.SendBuffer = baseIdent(args[1])
		}
		// tpcall(svc, send, sendlen, recv, recvlen, flags): args[3] is
		// the recv buffer. tpacall(svc, send, sendlen, flags): args[3]
		// is flags — never a buffer. Leave RecvBuffer empty for the
		// async variant; the reply arrives via tpgetrply.
		if len(args) > 3 && c.Name != "tpacall" {
			tc.RecvBuffer = baseIdent(args[3])
		}
		lo, hi := enclosingSpan(facts, c)
		for _, op := range ops {
			switch {
			case op.Line < c.Line && op.Line >= lo && op.Buffer != "" && op.Buffer == tc.SendBuffer:
				if op.Kind == FmlAdd {
					tc.SendFML = append(tc.SendFML, op)
				}
			case op.Line > c.Line && op.Line <= hi && op.Buffer != "" && op.Buffer == tc.RecvBuffer:
				if op.Kind == FmlGet {
					tc.RecvFML = append(tc.RecvFML, op)
					if op.Line > tc.EndLine {
						tc.EndLine = op.Line
					}
				}
			}
		}
		tc.Ambiguous = tc.SendBuffer != "" && tc.RecvBuffer != "" &&
			len(tc.SendFML) == 0 && len(tc.RecvFML) == 0
		out = append(out, tc)
	}
	return out
}

// enclosingSpan is the innermost branch block containing the call, else the
// enclosing function body, else the whole file.
func enclosingSpan(facts *tsscan.SourceFacts, c *tsscan.FunctionCall) (int, int) {
	lo, hi := 1, facts.NumLines
	if c.Func != "" {
		for i := range facts.Functions {
			fn := &facts.Functions[i]
			if fn.Name == c.Func && fn.BodyStartLine > 0 {
				lo = fn.BodyStartLine
				if fn.BodyEndLine > 0 {
					hi = fn.BodyEndLine
				}
			}
		}
	}
	// innermost braced branch block containing the call line
	for i := range facts.Branches {
		b := &facts.Branches[i]
		if b.BlockStart == 0 || b.BlockEnd == 0 {
			continue
		}
		if b.BlockStart <= c.Line && c.Line <= b.BlockEnd {
			if b.BlockStart >= lo && b.BlockEnd <= hi {
				lo, hi = b.BlockStart, b.BlockEnd
			}
		}
	}
	return lo, hi
}
