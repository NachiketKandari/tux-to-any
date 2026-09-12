package tsscan

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	c "github.com/tree-sitter/tree-sitter-c/bindings/go"
)

type walker struct {
	src       []byte
	facts     *SourceFacts
	depth     int
	fn        string
	inBody    bool
	unmatched []pos
}

func newParser() *sitter.Parser {
	p := sitter.NewParser()
	p.SetLanguage(sitter.NewLanguage(c.Language()))
	return p
}

func nodeText(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

func childByKind(n *sitter.Node, kinds ...string) *sitter.Node {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		for _, k := range kinds {
			if c.Kind() == k {
				return c
			}
		}
	}
	return nil
}

// typeText joins the leading type-ish children of a declaration (struct
// bodies are trimmed away).
func typeText(src []byte, n *sitter.Node, stopAt int) string {
	parts := make([]string, 0, 2)
	for i := 0; i < stopAt; i++ {
		c := n.Child(uint(i))
		switch c.Kind() {
		case "primitive_type", "type_identifier", "sized_type_specifier",
			"struct_specifier", "union_specifier", "enum_specifier":
			t := nodeText(src, c)
			if idx := strings.IndexByte(t, '{'); idx >= 0 {
				t = t[:idx]
			}
			parts = append(parts, strings.TrimSpace(t))
		}
	}
	return strings.Join(parts, " ")
}

var declaratorKinds = map[string]bool{
	"identifier": true, "init_declarator": true, "pointer_declarator": true,
	"array_declarator": true, "function_declarator": true, "parenthesized_declarator": true,
	"attributed_declarator": true,
}

// walkDeclarator extracts (name, array, nameNode) from a declarator chain.
func walkDeclarator(src []byte, n *sitter.Node) (string, bool, *sitter.Node) {
	switch n.Kind() {
	case "identifier":
		return nodeText(src, n), false, n
	case "init_declarator", "pointer_declarator", "parenthesized_declarator", "attributed_declarator":
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if declaratorKinds[c.Kind()] {
				return walkDeclarator(src, c)
			}
		}
	case "array_declarator":
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if declaratorKinds[c.Kind()] {
				name, _, idn := walkDeclarator(src, c)
				return name, true, idn
			}
		}
	case "function_declarator":
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c.Kind() == "parameter_list" {
				continue
			}
			if declaratorKinds[c.Kind()] {
				return walkDeclarator(src, c)
			}
		}
	}
	return "", false, nil
}

// recordDeclaration records one declaration's VarDecls (one per declarator).
// Works for both statement declarations and parameter declarations.
func (w *walker) recordDeclaration(n *sitter.Node) {
	stop, typeDone := 0, false
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(uint(i))
		if declaratorKinds[c.Kind()] {
			stop, typeDone = i, true
			break
		}
		switch c.Kind() {
		case "struct_specifier", "enum_specifier", "union_specifier":
			if childByKind(c, "field_declaration_list", "enumerator_list") != nil {
				stop, typeDone = i+1, true
			}
		}
		if typeDone {
			break
		}
	}
	if !typeDone {
		return
	}
	base := typeText(w.src, n, stop)
	for i := stop; i < int(n.ChildCount()); i++ {
		c := n.Child(uint(i))
		if c.Kind() == "function_declarator" {
			// a prototype declaration: its parameters still type host vars
			w.recordParams(c)
			continue
		}
		if hasPointerDeclarator(c) {
			continue // pointer declarations are not VarDecls (pinned vocabulary)
		}
		name, array, idn := walkDeclarator(w.src, c)
		if name == "" || idn == nil {
			continue
		}
		sp := idn.StartPosition()
		w.facts.VarDecls = append(w.facts.VarDecls, VarDecl{
			Type: base, Name: name,
			Line: int(sp.Row) + 1, Col: int(sp.Column) + 1,
			Array: array, Func: w.fnCtx(),
		})
	}
}

// hasPointerDeclarator reports whether the declarator chain dereferences.
func hasPointerDeclarator(n *sitter.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind() == "pointer_declarator" {
		return true
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if hasPointerDeclarator(n.Child(i)) {
			return true
		}
	}
	return false
}

// fnCtx is the function context: file-scope declarations and parameters
// record with "".
func (w *walker) fnCtx() string {
	if w.inBody {
		return w.fn
	}
	return ""
}

// walk dispatches on node kind; every handler is responsible for recursing
// into exactly the children it owns.
func (w *walker) walk(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "function_definition":
		w.walkFunctionDefinition(n)
	case "compound_statement":
		w.depth++
		w.walkChildren(n)
		w.depth--
	case "declaration":
		w.recordDeclaration(n)
		w.walkChildrenFrom(n, firstDeclaratorIndex(n))
	case "if_statement":
		w.walkIf(n, BranchIf)
	case "while_statement":
		w.walkWhile(n)
	case "for_statement":
		w.walkFor(n)
	case "do_statement":
		w.walkDo(n)
	case "ERROR":
		w.recoverTailLessDo(n)
		w.walkChildren(n)
	case "return_statement":
		sp := n.StartPosition()
		w.facts.Returns = append(w.facts.Returns, Return{
			Line: int(sp.Row) + 1, Col: int(sp.Column) + 1, Func: w.fnCtx(),
		})
		w.walkChildren(n)
	case "call_expression":
		w.recordCall(n)
		w.walkChildren(n)
	case "preproc_include":
		w.recordInclude(n)
	case "preproc_def", "preproc_function_def", "preproc_call", "preproc_if",
		"preproc_ifdef", "preproc_else", "preproc_elif", "preproc_endif":
		w.recordDirective(n)
		w.walkChildren(n)
	default:
		w.walkChildren(n)
	}
}

func firstDeclaratorIndex(n *sitter.Node) int {
	for i := 0; i < int(n.ChildCount()); i++ {
		if declaratorKinds[n.Child(uint(i)).Kind()] {
			return i
		}
	}
	return int(n.ChildCount())
}

// childAt returns the child at index i, or nil when out of range.
func childAt(n *sitter.Node, i int) *sitter.Node {
	if i < 0 || i >= int(n.ChildCount()) {
		return nil
	}
	return n.Child(uint(i))
}

// nextNonComment returns the first child at or after index i that is not a
// comment node (comments are extra named children in the tree).
func nextNonComment(n *sitter.Node, i int) *sitter.Node {
	for ; i < int(n.ChildCount()); i++ {
		c := n.Child(uint(i))
		if c.Kind() != "comment" {
			return c
		}
	}
	return nil
}

func (w *walker) walkChildren(n *sitter.Node) {
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walk(n.Child(i))
	}
}

func (w *walker) walkChildrenFrom(n *sitter.Node, from int) {
	for i := from; i < int(n.ChildCount()); i++ {
		w.walk(n.Child(uint(i)))
	}
}

// walkFunctionDefinition records the function and its parameters, then the
// body at brace depth 1 with the function context active. BodyEndLine is 0
// when the body's opening brace never closes (unbalanced file).
func (w *walker) walkFunctionDefinition(n *sitter.Node) {
	var decl *sitter.Node
	var typeStop int
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(uint(i))
		if c.Kind() == "function_declarator" {
			decl, typeStop = c, i
			break
		}
	}
	if decl == nil {
		w.walkChildren(n)
		return
	}
	retType := typeText(w.src, n, typeStop)
	name, _, idn := walkDeclarator(w.src, decl)
	if name == "" || idn == nil {
		w.walkChildren(n)
		return
	}
	sp := idn.StartPosition()
	fnDef := FunctionDef{
		Name: name, ReturnType: retType,
		StartLine: int(sp.Row) + 1, Col: int(sp.Column) + 1,
	}
	w.facts.Functions = append(w.facts.Functions, fnDef)

	prevFn, prevIn := w.fn, w.inBody
	w.recordParams(decl)
	body := childByKind(n, "compound_statement")
	w.fn, w.inBody = name, true
	if body != nil {
		fnDef.BodyStartLine = int(body.StartPosition().Row) + 1
		if w.unmatchedAt(body.StartPosition()) {
			fnDef.BodyEndLine = 0
		} else {
			fnDef.BodyEndLine = int(body.EndPosition().Row) + 1
		}
		w.facts.Functions[len(w.facts.Functions)-1] = fnDef
		w.walk(body)
	}
	w.fn, w.inBody = prevFn, prevIn
}

// recordParams records parameter declarations in the Params side-channel
// (the pinned vocabulary keeps parameters out of VarDecls but consumers
// still need them for host-variable typing).
func (w *walker) recordParams(decl *sitter.Node) {
	pl := childByKind(decl, "parameter_list")
	if pl == nil {
		return
	}
	for i := uint(0); i < pl.NamedChildCount(); i++ {
		if pd := pl.NamedChild(i); pd.Kind() == "parameter_declaration" {
			w.recordParamDecl(pd)
		}
	}
}

func (w *walker) recordParamDecl(n *sitter.Node) {
	stop, typeDone := 0, false
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(uint(i))
		if declaratorKinds[c.Kind()] {
			stop, typeDone = i, true
			break
		}
	}
	if !typeDone {
		return
	}
	base := typeText(w.src, n, stop)
	for i := stop; i < int(n.ChildCount()); i++ {
		// pointer parameters keep their base type (the pointer itself is
		// dropped, matching the pinned host-variable typing)
		name, array, idn := walkDeclarator(w.src, n.Child(uint(i)))
		if name == "" || idn == nil {
			continue
		}
		sp := idn.StartPosition()
		w.facts.Params = append(w.facts.Params, VarDecl{
			Type: base, Name: name,
			Line: int(sp.Row) + 1, Col: int(sp.Column) + 1,
			Array: array,
		})
	}
}

func (w *walker) unmatchedAt(p sitter.Point) bool {
	for _, up := range w.unmatched {
		if up.line == int(p.Row)+1 && up.col == int(p.Column)+1 {
			return true
		}
	}
	return false
}

func condNodeOf(n *sitter.Node) *sitter.Node {
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == "parenthesized_expression" {
			return c
		}
	}
	return nil
}

// condText is the condition text between the outer parens, whitespace-collapsed.
func condText(src []byte, n *sitter.Node) string {
	if n == nil || n.ChildCount() < 2 {
		return ""
	}
	return collapseWS(string(src[n.StartByte()+1 : n.EndByte()-1]))
}

// walkIf records an if/else-if arm and flattens its else-if/else chain so
// chain members stay siblings.
func (w *walker) walkIf(n *sitter.Node, kind BranchKind) {
	condIdx := -1
	for i := uint(0); i < n.ChildCount(); i++ {
		if n.Child(i).Kind() == "parenthesized_expression" {
			condIdx = int(i)
			break
		}
	}
	var condNode *sitter.Node
	if condIdx >= 0 {
		condNode = n.Child(uint(condIdx))
	}
	br := Branch{Kind: kind, Cond: condText(w.src, condNode)}
	sp := n.StartPosition()
	br.StartLine, br.StartCol = int(sp.Row)+1, int(sp.Column)+1
	if consequence := nextNonComment(n, condIdx+1); consequence != nil && consequence.Kind() == "compound_statement" {
		bsp, bep := consequence.StartPosition(), consequence.EndPosition()
		br.BlockStart, br.BlockStartCol = int(bsp.Row)+1, int(bsp.Column)+1
		br.BlockEnd, br.BlockEndCol = int(bep.Row)+1, int(bep.Column)
	}
	br.Depth = w.depth
	br.Function = w.fnCtx()
	w.facts.Branches = append(w.facts.Branches, br)

	if condNode != nil {
		w.walk(condNode) // calls inside the condition are recorded
	}
	// consequence: the statement between the condition and the else clause
	if consequence := nextNonComment(n, condIdx+1); consequence != nil && consequence.Kind() != "else_clause" {
		w.walk(consequence)
	}

	var elseTok, alt *sitter.Node
	if ec := childByKind(n, "else_clause"); ec != nil {
		for i := uint(0); i < ec.ChildCount(); i++ {
			c := ec.Child(i)
			switch c.Kind() {
			case "else":
				elseTok = c
			case "comment":
				// comments ride inside the else clause; skip them
			default:
				if alt == nil {
					alt = c
				}
			}
		}
	}
	if alt == nil {
		return
	}
	switch alt.Kind() {
	case "if_statement":
		w.walkIf(alt, BranchElseIf)
	default:
		els := Branch{Kind: BranchElse, Depth: w.depth, Function: w.fnCtx()}
		if elseTok != nil {
			esp := elseTok.StartPosition()
			els.StartLine, els.StartCol = int(esp.Row)+1, int(esp.Column)+1
		}
		if alt.Kind() == "compound_statement" {
			asp, aep := alt.StartPosition(), alt.EndPosition()
			els.BlockStart, els.BlockStartCol = int(asp.Row)+1, int(asp.Column)+1
			els.BlockEnd, els.BlockEndCol = int(aep.Row)+1, int(aep.Column)
		}
		w.facts.Branches = append(w.facts.Branches, els)
		w.walk(alt)
	}
}

// walkWhile records a while loop.
func (w *walker) walkWhile(n *sitter.Node) {
	sp := n.StartPosition()
	body := childByKind(n, "compound_statement")
	lp := Loop{
		Kind:      LoopWhile,
		StartLine: int(sp.Row) + 1, StartCol: int(sp.Column) + 1,
		Depth: w.depth, Function: w.fnCtx(),
	}
	if cond := condNodeOf(n); cond != nil {
		lp.Cond = condText(w.src, cond)
	}
	if body != nil {
		bsp, bep := body.StartPosition(), body.EndPosition()
		lp.BlockStart, lp.BlockStartCol = int(bsp.Row)+1, int(bsp.Column)+1
		lp.BlockEnd, lp.BlockEndCol = int(bep.Row)+1, int(bep.Column)
	}
	w.facts.Loops = append(w.facts.Loops, lp)
	w.walkChildren(n) // condition calls and body
}

// walkFor records a for loop; Cond is the full header between the parens
// with all whitespace stripped.
func (w *walker) walkFor(n *sitter.Node) {
	sp := n.StartPosition()
	body := childByKind(n, "compound_statement")
	lp := Loop{
		Kind:      LoopFor,
		StartLine: int(sp.Row) + 1, StartCol: int(sp.Column) + 1,
		Depth: w.depth, Function: w.fnCtx(),
	}
	if open, close := childToken(n, "(", firstPick), childToken(n, ")", lastPick); open != nil && close != nil {
		lp.Cond = stripWS(string(w.src[open.EndByte():close.StartByte()]))
	}
	if body != nil {
		bsp, bep := body.StartPosition(), body.EndPosition()
		lp.BlockStart, lp.BlockStartCol = int(bsp.Row)+1, int(bsp.Column)+1
		lp.BlockEnd, lp.BlockEndCol = int(bep.Row)+1, int(bep.Column)
	}
	w.facts.Loops = append(w.facts.Loops, lp)
	w.walkChildren(n)
}

// walkDo records a do-while as one loop whose tail while() fills Cond and
// WhileLine.
func (w *walker) walkDo(n *sitter.Node) {
	sp := n.StartPosition()
	body := childByKind(n, "compound_statement")
	lp := Loop{
		Kind:      LoopDo,
		StartLine: int(sp.Row) + 1, StartCol: int(sp.Column) + 1,
		Depth: w.depth, Function: w.fnCtx(),
	}
	if cond := condNodeOf(n); cond != nil {
		lp.Cond = condText(w.src, cond)
		csp := cond.StartPosition()
		lp.WhileLine = int(csp.Row) + 1
	}
	if body != nil {
		bsp, bep := body.StartPosition(), body.EndPosition()
		lp.BlockStart, lp.BlockStartCol = int(bsp.Row)+1, int(bsp.Column)+1
		lp.BlockEnd, lp.BlockEndCol = int(bep.Row)+1, int(bep.Column)
	}
	w.facts.Loops = append(w.facts.Loops, lp)
	w.walkChildren(n)
}

// recoverTailLessDo repairs the tail-less do the grammar cannot parse
// (pinned behavior): an ERROR node holding a bare `do` token
// followed by its compound body still records one do loop — the block
// extents are the body's, the tail condition stays empty.
func (w *walker) recoverTailLessDo(n *sitter.Node) {
	var doTok *sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(uint(i))
		if c.Kind() == "do" {
			doTok = c
			break
		}
	}
	if doTok == nil {
		return
	}
	body := firstCompoundAfterByte(n, doTok.StartByte())
	if body == nil {
		return
	}
	sp := doTok.StartPosition()
	lp := Loop{
		Kind: LoopDo, StartLine: int(sp.Row) + 1, StartCol: int(sp.Column) + 1,
		Depth: w.depth, Function: w.fnCtx(),
	}
	bsp, bep := body.StartPosition(), body.EndPosition()
	lp.BlockStart, lp.BlockStartCol = int(bsp.Row)+1, int(bsp.Column)+1
	lp.BlockEnd, lp.BlockEndCol = int(bep.Row)+1, int(bep.Column)
	w.facts.Loops = append(w.facts.Loops, lp)
}

// firstCompoundAfterByte finds the nearest compound_statement at or under n
// starting after byte offset from (the do body is a sibling of the `do`
// token, often one error-recovery level down).
func firstCompoundAfterByte(n *sitter.Node, from uint) *sitter.Node {
	if n.StartByte() > from && n.Kind() == "compound_statement" {
		return n
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := firstCompoundAfterByte(n.Child(uint(i)), from); c != nil {
			return c
		}
	}
	return nil
}

func childToken(n *sitter.Node, tok string, pick func(a, b *sitter.Node) *sitter.Node) *sitter.Node {
	var found *sitter.Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() && c.Kind() == tok {
			found = pick(found, c)
		}
	}
	return found
}

func firstPick(a, b *sitter.Node) *sitter.Node {
	if a == nil {
		return b
	}
	return a
}

func lastPick(a, b *sitter.Node) *sitter.Node { return b }

func stripWS(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if !isSpaceByte(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// recordCall records one call site; nested calls are covered by the generic
// recursion into the arguments.
func (w *walker) recordCall(n *sitter.Node) {
	fnNode := childByKind(n, "identifier")
	if fnNode == nil {
		return
	}
	name := nodeText(w.src, fnNode)
	args := ""
	if argList := childByKind(n, "argument_list"); argList != nil && argList.ChildCount() >= 2 {
		args = string(w.src[argList.StartByte()+1 : argList.EndByte()-1])
	}
	sp := fnNode.StartPosition()
	w.facts.Calls = append(w.facts.Calls, FunctionCall{
		Name: name,
		Line: int(sp.Row) + 1, Col: int(sp.Column) + 1,
		Args:      args,
		IsTpCall:  name == "tpcall",
		IsFnPref:  strings.HasPrefix(name, "fn_"),
		IsChkPref: strings.HasPrefix(name, "chk_"),
		Func:      w.fnCtx(),
	})
}

func (w *walker) recordInclude(n *sitter.Node) {
	arg := ""
	isSystem := false
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() == "system_lib_string" {
			arg, isSystem = nodeText(w.src, c), true
			break
		}
		if c.Kind() == "string_literal" {
			arg = nodeText(w.src, c)
			break
		}
	}
	sp := n.StartPosition()
	w.facts.Directives = append(w.facts.Directives, Directive{
		Kind: "include", Arg: arg, Line: int(sp.Row) + 1, IsHeader: true, IsSystem: isSystem,
	})
}

// recordDirective records raw preprocessor facts. #define arguments are the
// reconstructed rest-of-line text ("NAME VALUE" / "NAME(params) VALUE");
// every other directive keeps its raw spelling and is never interpreted.
func (w *walker) recordDirective(n *sitter.Node) {
	sp := n.StartPosition()
	line := int(sp.Row) + 1
	switch n.Kind() {
	case "preproc_def", "preproc_function_def":
		// the raw rest-of-line after "#define" is the fact ("NAME VALUE" /
		// "NAME(params) VALUE"), exactly as pinned in the goldens
		text := nodeText(w.src, n)
		if idx := strings.Index(text, "define"); idx >= 0 {
			text = text[idx+len("define"):]
		}
		if arg := strings.Trim(stripTrailingComment(text), " \t\r\n"); arg != "" {
			w.facts.Directives = append(w.facts.Directives, Directive{Kind: "define", Arg: arg, Line: line})
		}
	case "preproc_call":
		kind, arg := "", ""
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if i == 0 {
				kind = strings.TrimPrefix(nodeText(w.src, c), "#")
				continue
			}
			if c.Kind() == "preproc_arg" {
				arg = strings.TrimSpace(nodeText(w.src, c))
			}
		}
		if kind != "" {
			w.facts.Directives = append(w.facts.Directives, Directive{Kind: kind, Arg: arg, Line: line})
		}
	case "preproc_ifdef", "preproc_ifndef", "preproc_if":
		kind := map[string]string{"preproc_ifdef": "ifdef", "preproc_ifndef": "ifndef", "preproc_if": "if"}[n.Kind()]
		arg := ""
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c.Kind() == "identifier" {
				arg = nodeText(w.src, c)
				break
			}
		}
		w.facts.Directives = append(w.facts.Directives, Directive{Kind: kind, Arg: arg, Line: line})
	case "preproc_else", "preproc_elif", "preproc_endif":
		kind := map[string]string{"preproc_else": "else", "preproc_elif": "elif", "preproc_endif": "endif"}[n.Kind()]
		w.facts.Directives = append(w.facts.Directives, Directive{Kind: kind, Line: line})
	}
}
