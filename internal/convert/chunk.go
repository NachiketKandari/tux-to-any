package convert

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/common"
	"tux-to-any/internal/gen"
	"tux-to-any/internal/goast"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/ledger"
	"tux-to-any/internal/llm"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/telemetry"
	"tux-to-any/internal/validate"
)

// Chunked controller generation (2026-09-13, user directive): when one
// endpoint's assembled prompt exceeds the budget ceiling, the legacy view is
// split into contiguous statement-balanced fragments sized per chunk; each
// fragment is one bounded-retry llm.RunSeam call (audit artifacts suffixed
// #chunk<k>); the accepted fragments concatenate into the method body the
// full REQUIRED-CALLS + parse gates judge. Deterministic chunking — the LLM
// only translates a fragment, never decides where it ends.

// chunkSafetyPct keeps each chunk's estimated tokens at this share of the
// prompt ceiling: the chars/token estimator undercounts dense C code (a
// dense monolith slice measured ~2.8 chars/token at the provider), so the
// margin absorbs the estimation error.
const chunkSafetyPct = 70

// outputExpansionPct is the Go-translation inflation over the legacy view's
// own token estimate: the Go body carries the same statements plus error
// checks per store call and struct-literal shaping per output field, while
// the Pro*C error choreography (errlog/Fadd/tpfree/tpreturn) shrinks to
// `if err != nil`. Calibrated on the 2026-09-14 dense-C run — the F branch's
// completion measured ~1.2–1.4× its post-replacement view's tokens — 130 is
// mid-range and errs toward splitting earlier.
const outputExpansionPct = 130

// outputTriggerPct routes to fragments when the estimate reaches this share
// of the output ceiling: the chars/token ratio is configured, not measured,
// and the provider's tokenizer on dense C code ran ~2.8 chars/token against
// the default 4 (2026-09-14 dense-C run — the estimate read 3360 of 4000,
// the call truncated at exactly 4000). Splitting a branch that would have
// fit costs one extra call; a truncated body costs the call plus every
// retry — the margin errs the same way.
const outputTriggerPct = 80

// outputChunkReason names the output-ceiling split trigger, empty when the
// estimate fits with margin.
func outputChunkReason(b budget.Budget, viewSrc string, maxOutputTokens int) string {
	if maxOutputTokens <= 0 {
		return ""
	}
	est := outputTokenEstimate(b, viewSrc)
	if est > maxOutputTokens*outputTriggerPct/100 {
		return fmt.Sprintf("expected output of ~%d tokens approaches the %d-token ceiling", est, maxOutputTokens)
	}
	return ""
}

// outputTokenEstimate projects the controller body's token size from the
// post-replacement view source: view tokens inflated by the translation
// factor. It drives the output-ceiling routing decision — an estimate over
// MaxOutputTokens routes to the chunked path before a single call can
// truncate (finish_reason=length, gates reject, retries burn).
func outputTokenEstimate(b budget.Budget, viewSrc string) int {
	return b.Count(viewSrc) * outputExpansionPct / 100
}

// minFragmentChars is the smallest slice a chunk may carry; below it the
// scaffolding alone starves the fragment and the run fails loudly instead of
// fanning out hundreds of calls.
const minFragmentChars = 2000

// chunkFramingChars is head-room for the per-chunk framing the single-call
// scaffold cannot see (fragment banner, locals list, filtered sections).
const chunkFramingChars = 600

// chunkCtx carries everything the chunked path needs from controllerBody —
// the single-call path already computed these pieces.
type chunkCtx struct {
	ctx    context.Context
	opts   Options
	res    *Result
	svc    *gen.Service
	unit   plan.Unit
	db     map[string]dbOut
	cond   *ir.Condition
	view   budget.View
	scen   *scenPrompt
	prompt string // the full single-call prompt (base-scaffold accounting)
	calls  map[string]budget.DBCall
}

// systemPromptFragment is the fragment-framed system prompt: the same rules
// as systemPrompt, scoped to one contiguous fragment of the method body.
const systemPromptFragment = `You convert one contiguous fragment of a legacy Pro*C/Tuxedo branch into Go statements of a larger controller method. The full branch was too large for one prompt, so it arrives as ordered fragments — each is a run of consecutive statements, possibly cut mid-block (the surrounding braces arrive in their neighboring fragments; translate exactly the braces the fragment shows, never adding enclosing braces beyond them).
Rules:
- Emit ONLY the Go statements translating the fragment shown. No package, imports, or func declaration. No method wrapper. No helper functions, types, or constants.
- Never re-emit statements from other fragments; this fragment's statements follow the previously converted ones inside the same method body.
- Translate every brace line the fragment shows: a closing brace closes a block an earlier fragment opened; an if header whose closing brace lies beyond the fragment is closed by a later fragment. Never add enclosing braces beyond the fragment's own braces.
- Locals already declared by earlier fragments are listed — reuse them; never redeclare. Declare new locals with := on first use in THIS fragment.
- The signature is fixed and provided verbatim: the context parameter is c, the request parameter is request, and the named returns are data and err. Never declare or use req, resp, or Response.
- Read inputs only as request.<Field>, using the request struct's verbatim field names.
- The named returns are data and err — never shadow them: assign "data = ..." / "err = ..." (or fresh local names); "data := ..." inside the body discards the method's output.
- Emit an explicit "return data, err" / "return nil, err" ONLY where the fragment's own view shows a return; otherwise the fragment falls through to the next fragment — never append a closing return to satisfy the shape.
- Every store call the fragment shows appears exactly once — never repeat a call, never bare-call one whose results the view uses: capture the returned values into locals.
- Copy struct/row field names character-exact from the definitions provided — never fuse names (CToDateString is not CToDate + String) and never invent fields; a sql.NullString reads through its .String FIELD (no parentheses: row.X.String, never row.X.String()).
- Call the database exclusively through the store signatures provided — exactly the parameters each signature shows, same count and order. Never write SQL anywhere (no raw strings, no string literals containing SQL).
- Store methods return []*models.X or *models.X per their signatures; the row structs are provided verbatim — use their field names exactly, never invent fields.
- String comparisons use double-quoted literals: flag == "Y", never 'Y'.
- Every identifier must be one of: request.<Field>, a store call, data, err, a local you declare, or a local the earlier-fragments section lists. No hallucinated variables.
- The method template already emits the START and END debug logs — never write logger START/END statements, fmt.Print*, or any other logging in the body.
- Check every error: each call that returns err must be followed by if err != nil { return nil, err } before its results are used. Never swallow or ignore an error.
- Preserve the fragment's control flow exactly as shown — same loops, same branches, same order, verbatim nesting. A branch that looks dead still gets implemented. Every store call shown in the fragment MUST appear in your statements, under the same condition.
- On error return nil, err; on success return data, err — only where the fragment's view shows a return.`

// sliceScanner tracks brace/string/comment state across the flattened view's
// lines so statement boundaries land only at true depth-0 statement ends —
// braces inside string literals ("Folio # is {%s}") and comment text never
// shift the depth.
type sliceScanner struct {
	depth    int
	inBlock  bool // inside a /* */ comment (may span lines)
	inString bool
	inChar   bool
	escaped  bool
	sawCode  bool // the current line carried at least one code character
}

func (sc *sliceScanner) scanLine(line string) {
	sc.sawCode = false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case sc.inBlock:
			if ch == '*' && i+1 < len(line) && line[i+1] == '/' {
				sc.inBlock = false
				i++
			}
		case sc.inString:
			switch {
			case sc.escaped:
				sc.escaped = false
			case ch == '\\':
				sc.escaped = true
			case ch == '"':
				sc.inString = false
			}
		case sc.inChar:
			switch {
			case sc.escaped:
				sc.escaped = false
			case ch == '\\':
				sc.escaped = true
			case ch == '\'':
				sc.inChar = false
			}
		default:
			switch {
			case ch == '/' && i+1 < len(line) && line[i+1] == '*':
				sc.inBlock = true
				i++
			case ch == '/' && i+1 < len(line) && line[i+1] == '/':
				return // line comment — nothing code-shaped after this
			case ch == '"':
				sc.inString = true
			case ch == '\'':
				sc.inChar = true
			case ch == '{':
				sc.depth++
			case ch == '}':
				sc.depth--
			}
			if ch != ' ' && ch != '\t' && ch != '\r' {
				sc.sawCode = true
			}
		}
	}
}

// splitStatements cuts the flattened view into statement units at ANY
// nesting depth: each unit is a maximal run of lines representing one
// statement — ending at its `;` or block-closing `}`, `} else` chain
// continuations attached to their if, unbraced headers glued to their body,
// preprocessor continuations held open. A unit may end inside a braced
// block (mid-block fragments); validateFragment compensates the brace
// delta when parsing. Concatenating the units reproduces the view
// byte-for-byte (minus the dropped trailing method brace).
func splitStatements(view string) []string {
	lines := strings.Split(view, "\n")
	sc := &sliceScanner{}
	var units []string
	var cur []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		sc.scanLine(line)
		cur = append(cur, line)
		trimmed := strings.TrimSpace(line)
		endsStmt := strings.HasSuffix(trimmed, ";") || strings.HasSuffix(trimmed, "}")
		continuation := strings.HasSuffix(trimmed, "\\")
		if !sc.inBlock && sc.sawCode && endsStmt && !continuation {
			// Lookahead: an else-chain continuation (`} else ...`) belongs
			// to this statement's unit, so the unit stays open. Provenance
			// markers prefix every flattened line (`/*L21716*/}`), so the
			// peek strips them before classifying.
			if i+1 < len(lines) {
				next := stripLeadingComments(lines[i+1])
				if strings.HasPrefix(next, "}") || strings.HasPrefix(next, "else") {
					continue
				}
			}
			units = append(units, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	if len(cur) > 0 {
		units = append(units, strings.Join(cur, "\n"))
	}
	return dropTrailingMethodBrace(units)
}

// dropTrailingMethodBrace removes the entry function's own closing brace —
// the method template owns it, so it never belongs in a converted body.
// Provenance markers may prefix the brace line (`/*L21716*/}`), so the
// check strips block comments before comparing.
func dropTrailingMethodBrace(units []string) []string {
	if len(units) == 0 {
		return units
	}
	last := strings.Split(units[len(units)-1], "\n")
	for len(last) > 0 && braceOnlyLine(last[len(last)-1]) {
		last = last[:len(last)-1]
	}
	for len(last) > 0 && strings.TrimSpace(last[len(last)-1]) == "" {
		last = last[:len(last)-1]
	}
	if len(last) == 0 {
		return units[:len(units)-1]
	}
	units[len(units)-1] = strings.Join(last, "\n")
	return units
}

// braceOnlyLine reports whether a line's code content (block comments
// stripped) is exactly the method's closing brace.
func braceOnlyLine(line string) bool {
	s := line
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			s = s[:i]
			break
		}
		s = s[:i] + s[i+2+j+2:]
	}
	return strings.TrimSpace(s) == "}"
}

// stripLeadingComments trims leading block comments (the flattened render's
// `/*L<n>*​/` provenance markers) and whitespace from a line, so prefix
// classification sees the code beneath them.
func stripLeadingComments(line string) string {
	s := strings.TrimSpace(line)
	for strings.HasPrefix(s, "/*") {
		end := strings.Index(s, "*/")
		if end < 0 {
			break
		}
		s = strings.TrimSpace(s[end+2:])
	}
	return s
}

// groupFragments packs statement units into contiguous chunks under the
// per-chunk slice budget (chars). A unit larger than the budget becomes its
// own oversized chunk — the per-chunk budget check then fails loudly with
// the chunk named, never a silent truncation.
func groupFragments(units []string, sliceBudget int, sigChars func(string) int) []string {
	var chunks []string
	var cur []string
	curChars := 0
	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, strings.Join(cur, "\n"))
			cur = nil
			curChars = 0
		}
	}
	for _, u := range units {
		uc := len(u) + 1
		if len(cur) > 0 && curChars+uc+sigChars(u) > sliceBudget {
			flush()
		}
		cur = append(cur, u)
		curChars += len(u) + 1 + sigChars(u)
	}
	flush()
	return chunks
}

// fragmentLocals extracts the local names a fragment's Go declares (:= LHS
// and var statements) so later fragments can reuse them without
// redeclaring. Names are order-stable and capped to keep the prompt bounded.
func fragmentLocals(body string) []string {
	var names []string
	seen := map[string]bool{}
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, ":=") {
			lhs := strings.TrimSpace(strings.Split(line, ":=")[0])
			lhs = strings.TrimPrefix(lhs, "for ")
			for _, m := range identRe.FindAllString(lhs, -1) {
				add(m)
			}
			continue
		}
		if m := varDeclRe.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
	}
	return common.UniqueStable(names)
}

var (
	identRe   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	varDeclRe = regexp.MustCompile(`^\s*var\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

// filterByPresence keeps only the endpoint-level prompt items (constants
// `NAME = value`, error codes) whose key the chunk's own text mentions —
// the full lists (hundreds of legacy codes in a monolith) would starve
// every chunk.
func filterByPresence(items []string, text string) []string {
	var out []string
	for _, it := range items {
		key := it
		if i := strings.Index(it, " = "); i > 0 {
			key = it[:i]
		}
		if strings.Contains(text, key) {
			out = append(out, it)
		}
	}
	return out
}

// filterHelpers keeps the helper-mapping lines whose fn the chunk text calls.
func filterHelpers(helpers []string, text string) []string {
	var out []string
	for _, h := range helpers {
		if fn := strings.SplitN(h, "(", 2)[0]; strings.Contains(text, fn) {
			out = append(out, h)
		}
	}
	return out
}

// filterStubs keeps the stubbed-helper entries the chunk text calls.
func filterStubs(stubs []plan.Stub, text string) []plan.Stub {
	var out []plan.Stub
	for _, st := range stubs {
		if strings.Contains(text, st.Fn) {
			out = append(out, st)
		}
	}
	return out
}

// buildChunkPrompt assembles one fragment's prompt: the same deterministic
// sections as buildPrompt (DB contract, REQUIRED CALLS, struct definitions),
// scoped to the fragment, plus the fragment framing and the declared-locals
// continuation context.
func buildChunkPrompt(cx chunkCtx, k, n int, chunkText, dbContract, contract string, helpers, constants, errCodes []string, stubs []plan.Stub, locals []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Endpoint: %s\n\n", cx.unit.Name)
	fmt.Fprintf(&sb, "Fragment %d of %d — the legacy branch is larger than one prompt, so it is split into %d contiguous fragments of top-level statements, converted in order into ONE method body. Emit ONLY the Go statements translating the fragment below: no method wrapper, no package lines, no re-emission of other fragments, no closing `}` for the method.\n\n", k+1, n, n)
	if cx.scen != nil {
		fmt.Fprintf(&sb, "Scenario slice: %s — one dispatch-axis slice of the legacy entry; every contradicted branch is already folded away, so implement exactly what remains. Legacy declarations (int counters, EXEC SQL INCLUDE headers) are context only — Go declares nothing for them, translate statements only.\n\n", cx.scen.Key)
	}
	sb.WriteString("DB layer contract (call these; never write SQL):\n" + dbContract + "\n\n")
	if calls := requiredCalls(chunkText, "s.store."); len(calls) > 0 {
		sb.WriteString("REQUIRED CALLS — every one must appear in your statements, under the same condition the fragment shows: " +
			strings.Join(calls, ", ") + "\n\n")
	}
	if cx.scen != nil && k == 0 && len(cx.scen.Shared) > 0 {
		for _, s := range cx.scen.Shared {
			sb.WriteString("Shared blocks — " + s + "\n")
		}
		sb.WriteString("\n")
	}
	if cx.scen != nil && len(cx.scen.TxNotes) > 0 {
		sb.WriteString("Legacy transaction facts (SCEN evidence — preserve the transaction shape):\n")
		for _, tn := range cx.scen.TxNotes {
			sb.WriteString("  - " + tn + "\n")
		}
		sb.WriteString("\n")
	}
	if len(constants) > 0 {
		sb.WriteString("Legacy constants (preprocessor #defines visible in this fragment — use their literal values directly):\n")
		for _, cst := range constants {
			sb.WriteString("  - " + cst + "\n")
		}
		sb.WriteString("\n")
	}
	if len(errCodes) > 0 {
		sb.WriteString("Legacy error codes — retain them in the returned error text; the legacy runtime maps each code to its real message: " +
			strings.Join(errCodes, ", ") + "\n\n")
	}
	if len(helpers) > 0 {
		sb.WriteString("Legacy helper calls in the fragment — each resolved fn has a Go equivalent; never substitute one fn's symbol for another:\n")
		for _, h := range helpers {
			sb.WriteString("  - " + h + "\n")
		}
		sb.WriteString("\n")
	}
	if len(stubs) > 0 {
		sb.WriteString("Stubbed helpers — legacy fns with no source in the corpus. Each has a generated package-level stub (variadic args, int return, panics at runtime); call the RIGHT stub for each legacy fn and pass only identifiers your statements declare (declare zero-value locals for C-only names like c_ServiceName or error buffers; out-pointers become &local):\n")
		for _, st := range stubs {
			fmt.Fprintf(&sb, "  - %s(...) → %s(args ...any) int\n", st.Fn, common.CamelLowerGo(st.Fn))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Fixed method signature and verbatim struct definitions (parameter and field names must match exactly):\n" + contract + "\n\n")
	if k > 0 && len(locals) > 0 {
		sb.WriteString("Locals already declared by earlier fragments — never redeclare, reuse them: " +
			strings.Join(locals, ", ") + "\n\n")
	}
	fmt.Fprintf(&sb, "Legacy fragment %d of %d, with every SQL block already replaced by its store call:\n\n%s\n", k+1, n, chunkText)
	return sb.String()
}

// controllerBodyChunked runs the oversized-endpoint path: statement-boundary
// chunks, one bounded-retry seam call per chunk, fragment gates, then the
// combined body through the full parse + REQUIRED-CALLS gates. Any chunk or
// the combined gate failing fails the unit loudly with the chunk named.
func controllerBodyChunked(cx chunkCtx) (string, error) {
	opts := cx.opts
	receiver := receiverOf(opts)
	methods := requiredCalls(cx.view.Source, receiver)
	fullSigs := dbSignaturesFor(opts.Plan, cx.db, methods)

	sigLen := map[string]int{}
	for _, ln := range strings.Split(fullSigs, "\n") {
		if m := storeCallRe("s.store.").FindStringSubmatch(ln); m != nil {
			sigLen[m[1]] = len(ln) + 1
		}
	}
	helpersAll := legacyHelpers(opts.Plan, cx.view.Source)
	constantsAll := legacyConstants(opts.Main, cx.cond, opts.Main.Entry)
	errCodesAll := legacyErrorCodes(cx.cond)
	contract, err := cx.svc.ControllerPromptContext(cx.unit.Name, opts.Plan, methods)
	if err != nil {
		return "", err
	}

	// The per-chunk scaffold is what a chunk's prompt actually carries: the
	// shared contract plus the sections filtered to that chunk's own text
	// (its signature subset, the constants/codes/helpers/stubs it mentions).
	// Grouping budgets fragment + those extras — never the endpoint-level
	// unfiltered lists, which would starve every chunk.
	scenExtras := 0
	if cx.scen != nil {
		for _, tn := range cx.scen.TxNotes {
			scenExtras += len(tn) + 8
		}
	}
	extrasOf := func(unit string) int {
		seen := map[string]bool{}
		total := 0
		for _, call := range requiredCalls(unit, receiver) {
			if !seen[call] {
				seen[call] = true
				total += sigLen[call]
			}
		}
		for _, it := range filterByPresence(constantsAll, unit) {
			total += len(it) + 6
		}
		for _, it := range filterByPresence(errCodesAll, unit) {
			total += len(it) + 2
		}
		for _, h := range filterHelpers(helpersAll, unit) {
			total += len(h) + 6
		}
		for _, st := range filterStubs(opts.Plan.Stubs, unit) {
			total += len(st.Fn) + 48
		}
		return total
	}

	chunkTokens := opts.Budget.MaxPromptTokens * chunkSafetyPct / 100
	if chunkTokens < 1 {
		chunkTokens = 1
	}
	chunkChars := chunkTokens * opts.Budget.CharsPerToken
	sliceBudget := chunkChars - len(contract) - scenExtras - chunkFramingChars
	// The translated fragment must also fit the OUTPUT ceiling: a fragment
	// whose Go translation overflows the response cap truncates mid-body
	// (dropping trailing required calls into a gate loop). Cap the slice at
	// 75% of the output budget's char equivalent.
	outputRoom := opts.Budget.MaxOutputTokens * 3 / 4 * opts.Budget.CharsPerToken
	if outputRoom > 0 && sliceBudget > outputRoom {
		sliceBudget = outputRoom
	}
	if sliceBudget < minFragmentChars {
		return "", fmt.Errorf("convert: budget: prompt of %d tokens exceeds the %d-token ceiling and the prompt scaffolding leaves no fragment room (output ceiling %d tokens) — trim the mapping or raise run.maxPromptTokens/run.maxOutputTokens",
			opts.Budget.Count(cx.prompt), opts.Budget.MaxPromptTokens, opts.Budget.MaxOutputTokens)
	}

	chunks := groupFragments(splitStatements(cx.view.Source), sliceBudget, extrasOf)
	n := len(chunks)
	telemetry.Log(cx.ctx).Info("fragment plan",
		"unit", cx.unit.Name, "fragments", n, "slice_budget_chars", sliceBudget)
	var bodies []string
	for k, chunkText := range chunks {
		telemetry.Log(cx.ctx).Info("fragment dispatch",
			"unit", cx.unit.Name, "fragment", k+1, "of", n, "chars", len(chunkText))
		chunkMethods := requiredCalls(chunkText, receiver)
		dbContract := dbSignaturesFor(opts.Plan, cx.db, chunkMethods)
		prompt := buildChunkPrompt(cx, k, n, chunkText, dbContract, contract,
			filterHelpers(helpersAll, chunkText), filterByPresence(constantsAll, chunkText),
			filterByPresence(errCodesAll, chunkText), filterStubs(opts.Plan.Stubs, chunkText),
			fragmentLocals(strings.Join(bodies, "\n")))
		messages := func(notes []string) []llm.Message {
			msgs := []llm.Message{
				{Role: "system", Content: systemPromptFragment},
				{Role: "user", Content: prompt},
			}
			if len(notes) > 0 {
				msgs = append(msgs, llm.Message{
					Role:    "user",
					Content: "Your previous output failed validation:\n" + strings.Join(notes, "\n") + "\nFix these errors and emit only the corrected fragment statements.",
				})
			}
			return msgs
		}
		fragment, chatCalls, _, err := llm.RunSeam(cx.ctx, llm.SeamInput{
			Unit: cx.unit.ID, Kind: string(cx.unit.Kind), Name: fmt.Sprintf("%s#chunk%d", cx.unit.Name, k+1),
			Template: cx.unit.TemplateID, LLM: cx.unit.LLM,
			Audit: opts.Audit, Client: opts.Client, Budget: opts.Budget, MaxRetries: opts.MaxRetries,
			Temperature: 0.1,
			Prompt: func(attemptNotes []string) (string, []llm.Message) {
				return prompt, messages(attemptNotes)
			},
			Extract: cleanBody,
			Gate: func(body string) []string {
				verr := validateFragment(body)
				return append(verr, requiredCallErrs(chunkText, body, receiver)...)
			},
		})
		opts.Ledger.Get(cx.unit.ID, string(cx.unit.Kind), cx.unit.Name).Attempts += chatCalls
		cx.res.LLMCalls += chatCalls
		if err != nil {
			return "", fmt.Errorf("fragment %d/%d: %w", k+1, n, err)
		}
		bodies = append(bodies, fragment)
	}

	combined := strings.Join(bodies, "\n")
	var fullErrs []string
	fullErrs = append(fullErrs, validateBody(opts, combined)...)
	fullErrs = append(fullErrs, requiredCallErrs(cx.view.Source, combined, receiver)...)
	fullErrs = append(fullErrs, txGateErrs(combined, cx.calls)...)
	if len(fullErrs) > 0 {
		return "", fmt.Errorf("combined fragment body failed validation: %s", strings.Join(fullErrs, "; "))
	}
	opts.Ledger.Set(cx.unit.ID, ledger.StatusValidated, "")
	return combined, nil
}

// validateFragment parses a fragment in the same synthetic-method wrap
// validateBody uses, compensating the fragment's brace balance: a fragment
// cut mid-block closes deeper than it opens (or vice versa), so the parse
// check pads with synthetic braces — the pad is parse-only, never emitted;
// the combined body's full parse gate stays the real check.
func validateFragment(fragment string) []string {
	prefix, suffix := bracePads(fragment)
	wrapped := bodyParseWrap(strings.Repeat("{", prefix) + "\n" + fragment + "\n" + strings.Repeat("}", suffix))
	if _, ferr := goast.Emit("convert: controller fragment", wrapped); ferr != nil {
		return validate.TrimGoErrors(ferr.Error())
	}
	return nil
}

// bracePads returns the synthetic brace padding a fragment needs to parse in
// isolation: prefix braces when the text closes blocks opened before it
// (negative running depth), suffix braces when it ends with blocks still
// open. The pads never appear in the emitted body.
func bracePads(fragment string) (prefix, suffix int) {
	sc := &sliceScanner{}
	minDepth := 0
	for _, line := range strings.Split(fragment, "\n") {
		sc.scanLine(line)
		if sc.depth < minDepth {
			minDepth = sc.depth
		}
	}
	if minDepth < 0 {
		prefix = -minDepth
	}
	suffix = prefix + sc.depth
	if suffix < 0 {
		suffix = 0
	}
	return prefix, suffix
}
