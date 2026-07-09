package detect

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "Go"},
		{"src/app.PY", "Python"}, // extension case-insensitive
		{"Dockerfile", "Dockerfile"},
		{"build.gradle", "Gradle"},
		{"noext", ""},
		{"weird.xyz", ""},
	}
	for _, c := range cases {
		if got := Detect(c.path); got != c.want {
			t.Errorf("Detect(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestHistogram(t *testing.T) {
	paths := []string{"a.go", "b.go", "c.py", "README.md", "mystery"}
	got := Histogram(paths)
	if len(got) != 4 { // Go, Python, Markdown, Other
		t.Fatalf("got %d buckets, want 4: %v", len(got), got)
	}
	if got[0].Name != "Go" || got[0].FileCount != 2 {
		t.Errorf("top bucket = %+v, want Go x2", got[0])
	}
	if got[0].Share != 0.4 {
		t.Errorf("Go share = %v, want 0.4", got[0].Share)
	}
	for _, lc := range got {
		if lc.Share < 0 || lc.Share > 1 {
			t.Errorf("share out of range: %+v", lc)
		}
	}
}

func TestHistogramOnlyOtherIsDropped(t *testing.T) {
	if got := Histogram([]string{"mystery", "another"}); got != nil {
		t.Errorf("Other-only histogram should be nil, got %v", got)
	}
}
