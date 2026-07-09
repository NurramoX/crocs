// Package files implements `read-files` bundling. This is PLAN.md §2's
// XML carve-out from the JSON default — files' bodies are easier to read
// (and grep-follow-up easier to reason about) in XML than in JSON-escaped
// strings.
//
// Output shape:
//
//	<files crocs="1" command="read-files">
//	  <file path="..." lines="1-127">
//	    <![CDATA[
//	      1 | first line
//	      2 | second line
//	      ...
//	    ]]>
//	  </file>
//	  <file path="big.bin" error="..."/>
//	</files>
//
// Bodies are wrapped in CDATA so escaping is rarely needed; any literal
// `]]>` in the source is split into `]]]]><![CDATA[>`, the canonical CDATA
// escape idiom.
package files

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"crocs/internal/grepx"
	"crocs/internal/project"
)

// Request is one call to Bundle.
type Request struct {
	Root      string   // project root
	Paths     []string // relative paths to bundle
	LineRange string   // "200-300" or "200"; empty = whole file
	MaxSizeKB int      // skip files larger than this; 0 = use DefaultMaxSizeKB
}

// DefaultMaxSizeKB matches the Python crocs default so subagent prompts
// quoting flags keep working.
const DefaultMaxSizeKB = 100

// rangedReadMaxBytes is the size *floor* applied when the caller requested
// a --lines range: the effective limit is raised to it, and an explicit
// larger --max-size still wins. The emitted output is bounded by the range,
// not the file, so the whole-file --max-size guard would only force agents
// to bump a flag to read 50 lines of a 300KB file. Memory is the remaining
// concern; 10MB mirrors grepx's large-file cutoff.
const rangedReadMaxBytes = 10 << 20

// SchemaVersion is the value of the `crocs` attribute on the root <files>
// element. Mirrors output.SchemaVersion (PLAN.md §2 #1).
const SchemaVersion = "1"

// Bundle writes the XML document to w.
func Bundle(w io.Writer, req Request) error {
	maxSize := req.MaxSizeKB
	if maxSize <= 0 {
		maxSize = DefaultMaxSizeKB
	}
	maxBytes := int64(maxSize) * 1024

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	fmt.Fprintf(bw, `<files crocs="%s" command="read-files">`+"\n", SchemaVersion)
	for _, rel := range req.Paths {
		if err := writeOne(bw, req.Root, rel, req.LineRange, maxBytes); err != nil {
			return err
		}
	}
	fmt.Fprintln(bw, `</files>`)
	return nil
}

func writeOne(w io.Writer, root, rel, lineRange string, maxBytes int64) error {
	abs, err := project.ResolveInRoot(root, rel)
	if err != nil {
		writeErr(w, rel, err.Error())
		return nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeErr(w, rel, "stat: "+err.Error())
		return nil
	}
	if info.IsDir() {
		writeErr(w, rel, "is a directory")
		return nil
	}
	// A --lines request bounds the output by the range, so only the hard
	// memory cap applies; whole-file reads keep the --max-size contract.
	if lineRange != "" && maxBytes < rangedReadMaxBytes {
		maxBytes = rangedReadMaxBytes
	}
	if info.Size() > maxBytes {
		writeErr(w, rel, fmt.Sprintf("exceeds --max-size limit (%d bytes > %d bytes)",
			info.Size(), maxBytes))
		return nil
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		writeErr(w, rel, "read: "+err.Error())
		return nil
	}
	if !grepx.IsProbablyText(data) {
		// NUL bytes are illegal in XML 1.0 even inside CDATA — a raw binary
		// body would render the whole document unparseable.
		writeErr(w, rel, fmt.Sprintf("binary file (%d bytes), not emitted", len(data)))
		return nil
	}

	reqStart, reqEnd, err := parseLineRange(lineRange)
	if err != nil {
		writeErr(w, rel, "lines flag: "+err.Error())
		return nil
	}

	lines := splitLines(data)
	totalLines := len(lines)

	start, end := reqStart, reqEnd
	if end == 0 || end > totalLines {
		end = totalLines
	}
	if start <= 0 {
		start = 1
	}
	if start > end {
		// Requested range is entirely past EOF — emit an element so callers
		// know the file was seen, with a note instead of a nonsense range.
		fmt.Fprintf(w, `  <file path=%s note=%s/>`+"\n",
			xmlAttr(rel), xmlAttr(fmt.Sprintf(
				"requested lines %d-%d out of range: file has %d lines",
				reqStart, reqEnd, totalLines)))
		return nil
	}

	fmt.Fprintf(w, `  <file path=%s lines="%d-%d">`+"\n",
		xmlAttr(rel), start, end)
	fmt.Fprint(w, "<![CDATA[\n")
	width := numWidth(end)
	for i := start; i <= end; i++ {
		line := lines[i-1]
		// CDATA escape: `]]>` inside content closes the section. Split it.
		line = strings.ReplaceAll(line, "]]>", "]]]]><![CDATA[>")
		fmt.Fprintf(w, "%*d | %s\n", width, i, line)
	}
	fmt.Fprint(w, "]]>\n")
	fmt.Fprint(w, "  </file>\n")
	return nil
}

func writeErr(w io.Writer, path, msg string) {
	fmt.Fprintf(w, `  <file path=%s error=%s/>`+"\n",
		xmlAttr(path), xmlAttr(msg))
}

// xmlAttr returns a properly-quoted attribute value with `&`, `<`, `>`,
// `"`, and `'` escaped.
func xmlAttr(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return `"` + r.Replace(s) + `"`
}

func parseLineRange(s string) (start, end int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		a, err1 := strconv.Atoi(strings.TrimSpace(s[:i]))
		b, err2 := strconv.Atoi(strings.TrimSpace(s[i+1:]))
		if err1 != nil || err2 != nil {
			return 0, 0, errors.New("invalid --lines range; expected START-END")
		}
		if a > b {
			return 0, 0, errors.New("--lines range start > end")
		}
		return a, b, nil
	}
	a, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, errors.New("invalid --lines value; expected START or START-END")
	}
	return a, a, nil
}

func splitLines(data []byte) []string {
	s := string(data)
	if s == "" {
		return nil
	}
	// strings.Split on "\n" preserves trailing empty when the file ends with
	// a newline — drop that artifact so line counts match what a user sees.
	out := strings.Split(s, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	// CRLF files: the \r is invisible noise inside CDATA — drop it.
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

func numWidth(n int) int {
	if n <= 0 {
		return 1
	}
	w := 0
	for n > 0 {
		w++
		n /= 10
	}
	return w
}
