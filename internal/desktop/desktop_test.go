package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseExec(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"firefox %u", []string{"firefox"}},
		{"firefox", []string{"firefox"}},
		{"/usr/bin/foo --bar %F", []string{"/usr/bin/foo", "--bar"}},
		{`sh -c "echo hello world"`, []string{"sh", "-c", "echo hello world"}},
		{`app --path="/a b/c"`, []string{"app", "--path=/a b/c"}},
		{`app --flag=%i name`, []string{"app", "--flag=", "name"}},
		{`app 100%% ready`, []string{"app", "100%", "ready"}},
		{`term -e 'a b'`, []string{"term", "-e", "a b"}},
		{`app "say \"hi\""`, []string{"app", `say "hi"`}},
	}
	for _, c := range cases {
		got := ParseExec(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseExec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseSkipsNonApps(t *testing.T) {
	base := "[Desktop Entry]\nType=Application\nName=Foo\nExec=foo\n"
	if _, ok := parse(strings.NewReader(base), "foo"); !ok {
		t.Error("a plain application was rejected")
	}
	for name, body := range map[string]string{
		"link":       "[Desktop Entry]\nType=Link\nName=Foo\nExec=foo\n",
		"nodisplay":  "[Desktop Entry]\nType=Application\nName=Foo\nExec=foo\nNoDisplay=true\n",
		"hidden":     "[Desktop Entry]\nType=Application\nName=Foo\nExec=foo\nHidden=true\n",
		"no-name":    "[Desktop Entry]\nType=Application\nExec=foo\n",
		"no-exec":    "[Desktop Entry]\nType=Application\nName=Foo\n",
		"othergroup": "[Other]\nType=Application\nName=Foo\nExec=foo\n",
	} {
		if _, ok := parse(strings.NewReader(body), name); ok {
			t.Errorf("%s: accepted, want skipped", name)
		}
	}
}

func TestScanDedupesAndSorts(t *testing.T) {
	home := t.TempDir()
	sys := t.TempDir()
	writeEntry(t, filepath.Join(home, "applications", "foo.desktop"), "Foo", "/home/foo")
	writeEntry(t, filepath.Join(home, "applications", "bar.desktop"), "Bar", "/home/bar")
	// A system entry with the same id must lose to the user one.
	writeEntry(t, filepath.Join(sys, "applications", "foo.desktop"), "Foo (system)", "/sys/foo")
	writeEntry(t, filepath.Join(sys, "applications", "zed.desktop"), "Zed", "/sys/zed")

	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_DATA_DIRS", sys)

	got := Scan()
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3 (%v)", len(got), got)
	}
	if got[0].Name != "Bar" || got[1].Name != "Foo" || got[2].Name != "Zed" {
		t.Errorf("order = %q, want [Bar Foo Zed] (sorted by name)", []string{got[0].Name, got[1].Name, got[2].Name})
	}
	if got[1].Exec[0] != "/home/foo" {
		t.Errorf("foo exec = %q, want the user entry to win", got[1].Exec)
	}
}

func TestScanNestedID(t *testing.T) {
	home := t.TempDir()
	writeEntry(t, filepath.Join(home, "applications", "sub", "deep.desktop"), "Deep", "/deep")
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	got := Scan()
	if len(got) != 1 || got[0].ID != "sub-deep.desktop" {
		t.Fatalf("got %+v, want id sub-deep.desktop", got)
	}
}

func writeEntry(t *testing.T, path, name, exec string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=" + exec + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
