package logs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBufferBoundsLinesAndBytes(t *testing.T) {
	buffer := NewBuffer(3, 12)
	buffer.Append("one", "two", "three", "four")
	if got, want := buffer.Lines(), []string{"three", "four"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Lines() = %v, want %v", got, want)
	}
	if buffer.Dropped() != 2 {
		t.Fatalf("Dropped() = %d", buffer.Dropped())
	}
}

func TestBufferTruncatesOversizedUTF8Line(t *testing.T) {
	buffer := NewBuffer(10, 8)
	buffer.Append("prefix-世界")
	lines := buffer.Lines()
	if len(lines) != 1 || len([]byte(lines[0])) > 7 {
		t.Fatalf("lines = %q", lines)
	}
}

func TestFilterWrapAndSave(t *testing.T) {
	buffer := NewBuffer(10, 100)
	buffer.Append("alpha match", "beta", "another MATCH")
	if got := buffer.Filtered("match"); len(got) != 2 {
		t.Fatalf("Filtered() = %v", got)
	}
	if got := Wrap([]string{"abcdef"}, 3); !reflect.DeepEqual(got, []string{"abc", "def"}) {
		t.Fatalf("Wrap() = %v", got)
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	if err := buffer.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha match\nbeta\nanother MATCH\n" {
		t.Fatalf("saved logs = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}
