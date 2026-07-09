package files

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: write a fixture file and return its dir + relative path.
func mkFixture(t *testing.T, name, body string) (root, rel string) {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, name
}

func TestBundleSingleFileIsValidXML(t *testing.T) {
	root, rel := mkFixture(t, "hello.go", "package main\n\nfunc main() {}\n")
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}

	// The output MUST parse as a single XML document — that's the whole point
	// of adding the root <files> wrapper in §2 #2.
	var probe struct {
		XMLName xml.Name `xml:"files"`
		Crocs   string   `xml:"crocs,attr"`
		Command string   `xml:"command,attr"`
		Files   []struct {
			Path string `xml:"path,attr"`
			Body string `xml:",chardata"`
		} `xml:"file"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &probe); err != nil {
		t.Fatalf("xml.Unmarshal: %v\n%s", err, buf.String())
	}
	if probe.Crocs != SchemaVersion {
		t.Errorf("root @crocs = %q, want %q", probe.Crocs, SchemaVersion)
	}
	if probe.Command != "read-files" {
		t.Errorf("root @command = %q, want %q", probe.Command, "read-files")
	}
	if len(probe.Files) != 1 {
		t.Fatalf("expected 1 <file>, got %d", len(probe.Files))
	}
	if probe.Files[0].Path != rel {
		t.Errorf("file @path = %q, want %q", probe.Files[0].Path, rel)
	}
	if !strings.Contains(probe.Files[0].Body, "package main") {
		t.Errorf("body missing source content:\n%s", probe.Files[0].Body)
	}
	// Line-number prefix should be present. Padding width tracks the max
	// emitted line, so for 3 lines we get "1 | …", not " 1 | …".
	if !strings.Contains(probe.Files[0].Body, "1 | package main") {
		t.Errorf("expected `1 | package main` line prefix:\n%s", probe.Files[0].Body)
	}
}

func TestBundleLineRange(t *testing.T) {
	body := strings.Repeat("xx\n", 50) // 50 lines
	root, rel := mkFixture(t, "long.txt", body)

	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}, LineRange: "10-15"}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if !strings.Contains(buf.String(), `lines="10-15"`) {
		t.Errorf("expected lines=\"10-15\" attribute:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "10 | xx") {
		t.Errorf("expected line 10 prefix:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), " 9 | xx") {
		t.Errorf("did not expect line 9 in output:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "16 | xx") {
		t.Errorf("did not expect line 16 in output:\n%s", buf.String())
	}
}

func TestBundleMaxSizeRejection(t *testing.T) {
	// Write ~3KB and set --max-size=1 (i.e. 1 KB).
	root, rel := mkFixture(t, "big.txt", strings.Repeat("0123456789\n", 300))

	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}, MaxSizeKB: 1}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if !strings.Contains(buf.String(), `error=`) {
		t.Errorf("expected error= attribute on oversized file:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "exceeds --max-size") {
		t.Errorf("expected oversize error message:\n%s", buf.String())
	}
}

func TestBundleCDATAEscape(t *testing.T) {
	// Source literally contains `]]>` — must not break the CDATA section.
	root, rel := mkFixture(t, "bad.js", "var re = /foo]]>bar/;\n")
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	// Still parseable as a single XML doc.
	var probe struct {
		XMLName xml.Name `xml:"files"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &probe); err != nil {
		t.Fatalf("xml broke on ]]> in content: %v\n%s", err, buf.String())
	}
}

func TestBundleMissingFile(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: dir, Paths: []string{"does-not-exist.txt"}}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	// The contract is: emit an error= element rather than abort the whole bundle.
	if !strings.Contains(buf.String(), `error=`) {
		t.Errorf("expected error= for missing file:\n%s", buf.String())
	}
}

func TestBundleRejectsTraversal(t *testing.T) {
	root, _ := mkFixture(t, "inner.txt", "safe\n")
	var buf bytes.Buffer
	err := Bundle(&buf, Request{Root: root, Paths: []string{"../../../../etc/passwd", "/etc/passwd"}})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "root:") {
		t.Fatalf("traversal path was read:\n%s", out)
	}
	if got := strings.Count(out, "error="); got != 2 {
		t.Errorf("expected 2 error= entries for escaping paths, got %d:\n%s", got, out)
	}
}

func TestBundleRejectsSymlinkEscape(t *testing.T) {
	root, _ := mkFixture(t, "inner.txt", "safe\n")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{"link.txt"}}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Errorf("symlink escape was read:\n%s", buf.String())
	}
}

func TestBundleBinarySkipped(t *testing.T) {
	root, rel := mkFixture(t, "blob.bin", "PK\x03\x04\x00\x00binary")
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if !strings.Contains(buf.String(), "binary file") {
		t.Errorf("expected binary-file error entry:\n%s", buf.String())
	}
	var probe struct {
		XMLName xml.Name `xml:"files"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &probe); err != nil {
		t.Fatalf("binary output broke XML: %v", err)
	}
}

func TestBundleRangePastEOF(t *testing.T) {
	root, rel := mkFixture(t, "short.txt", "one\ntwo\n")
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}, LineRange: "500-510"}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "out of range") || !strings.Contains(out, "file has 2 lines") {
		t.Errorf("expected out-of-range note:\n%s", out)
	}
	if strings.Contains(out, `lines="499`) {
		t.Errorf("inverted range attribute leaked:\n%s", out)
	}
}

func TestBundleRangeBypassesMaxSize(t *testing.T) {
	// 3KB file, --max-size 1KB, but only a 2-line slice requested: must emit
	// the slice instead of an oversize error.
	root, rel := mkFixture(t, "big.txt", strings.Repeat("0123456789\n", 300))
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}, MaxSizeKB: 1, LineRange: "10-11"}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "exceeds --max-size") {
		t.Errorf("ranged read should bypass --max-size:\n%s", out)
	}
	if !strings.Contains(out, "10 | 0123456789") {
		t.Errorf("expected line 10 in output:\n%s", out)
	}
}

func TestBundleCRLFTrimmed(t *testing.T) {
	root, rel := mkFixture(t, "dos.txt", "one\r\ntwo\r\n")
	var buf bytes.Buffer
	if err := Bundle(&buf, Request{Root: root, Paths: []string{rel}}); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if strings.Contains(buf.String(), "\r") {
		t.Errorf("carriage returns leaked into output:\n%q", buf.String())
	}
	if !strings.Contains(buf.String(), "1 | one") {
		t.Errorf("content missing:\n%s", buf.String())
	}
}

func TestParseLineRange(t *testing.T) {
	cases := []struct {
		in        string
		wantStart int
		wantEnd   int
		wantErr   bool
	}{
		{"", 0, 0, false},
		{"5", 5, 5, false},
		{"10-15", 10, 15, false},
		{"15-10", 0, 0, true},
		{"abc", 0, 0, true},
		{"10-", 0, 0, true},
		{"-5", 0, 0, true},
	}
	for _, c := range cases {
		start, end, err := parseLineRange(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseLineRange(%q): err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && (start != c.wantStart || end != c.wantEnd) {
			t.Errorf("parseLineRange(%q) = (%d, %d), want (%d, %d)",
				c.in, start, end, c.wantStart, c.wantEnd)
		}
	}
}
