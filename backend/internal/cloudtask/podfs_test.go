package cloudtask

import "testing"

func TestParseStatLines(t *testing.T) {
	out := "4096|1722400000|drwxr-xr-x|directory|uploads\n" +
		"12|1722400001|-rw-r--r--|regular file|notes.txt\n" +
		"0|1722400002|-rw-r--r--|regular empty file|empty\n" +
		"7|1722400003|lrwxrwxrwx|symbolic link|link\n" +
		"0|1722400004|srwxr-xr-x|socket|app.sock\n" +
		"garbage line\n" +
		"notanumber|1|x|regular file|bad\n"

	entries := parseStatLines(out)
	if len(entries) != 5 {
		t.Fatalf("len(entries)=%d want 5: %+v", len(entries), entries)
	}
	want := []struct {
		name string
		kind string
		size int64
	}{
		{"uploads", FileKindDir, 4096},
		{"notes.txt", FileKindFile, 12},
		{"empty", FileKindFile, 0},
		{"link", FileKindSymlink, 7},
		{"app.sock", FileKindOther, 0},
	}
	for i, w := range want {
		if entries[i].Name != w.name || entries[i].Kind != w.kind || entries[i].Size != w.size {
			t.Errorf("entry %d = %+v, want name=%s kind=%s size=%d", i, entries[i], w.name, w.kind, w.size)
		}
	}
	if entries[0].Mode != "drwxr-xr-x" || entries[0].ModTime != 1722400000 {
		t.Errorf("entry 0 mode/mtime = %q/%d", entries[0].Mode, entries[0].ModTime)
	}
}

func TestParseStatLinesKeepsSeparatorInName(t *testing.T) {
	entries := parseStatLines("5|1722400000|-rw-r--r--|regular file|weird|name.txt\n")
	if len(entries) != 1 {
		t.Fatalf("len(entries)=%d want 1", len(entries))
	}
	if entries[0].Name != "weird|name.txt" {
		t.Errorf("name=%q want %q", entries[0].Name, "weird|name.txt")
	}
}

func TestParseStatLinesSkipsDotEntries(t *testing.T) {
	entries := parseStatLines("4096|1|drwxr-xr-x|directory|.\n4096|1|drwxr-xr-x|directory|..\n")
	if len(entries) != 0 {
		t.Fatalf("len(entries)=%d want 0: %+v", len(entries), entries)
	}
}

func TestShellArgvKeepsPathsPositional(t *testing.T) {
	argv := sh(`cat -- "$1"`, "/data/'; rm -rf /; '")
	if len(argv) != 5 {
		t.Fatalf("argv=%v want 5 elements", argv)
	}
	if argv[0] != "sh" || argv[1] != "-c" || argv[3] != "_" {
		t.Fatalf("argv prefix = %v", argv[:4])
	}
	if argv[2] != `cat -- "$1"` {
		t.Errorf("script was rewritten: %q", argv[2])
	}
	if argv[4] != "/data/'; rm -rf /; '" {
		t.Errorf("path was not passed verbatim: %q", argv[4])
	}
}

func TestUnconfiguredPodFSFailsClosed(t *testing.T) {
	fs := unconfiguredPodFS{err: errNotInCluster}
	if fs.Enabled() {
		t.Fatal("Enabled() = true, want false")
	}
	if _, _, err := fs.List(t.Context(), PodTarget{}, "/data"); err == nil {
		t.Fatal("List() error = nil, want not-configured error")
	}
	if err := fs.WriteFile(t.Context(), PodTarget{}, "/data/x", nil); err == nil {
		t.Fatal("WriteFile() error = nil, want not-configured error")
	}
}

var errNotInCluster = errTest("in-cluster config: no service account")

type errTest string

func (e errTest) Error() string { return string(e) }
