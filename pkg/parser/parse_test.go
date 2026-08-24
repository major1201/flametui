package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/major1201/flametui/pkg/parser"
)

func TestParseStackcollapse(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "stackcollapse_data", "simple.txt"))
	if err != nil {
		t.Fatal(err)
	}

	prof, err := parser.Parse(data, "simple.txt")
	if err != nil {
		t.Fatal(err)
	}

	if prof.RootStack == nil {
		t.Fatal("root stack is nil")
	}

	// a;b;c appears 5 times (1+1+3), a;b;d appears 4 times, a;b appears 5 times
	// Total: 5 + 4 + 5 = 14
	expectedTotal := int64(14)
	if prof.RootStack.Values[0] != expectedTotal {
		t.Errorf("expected total %d, got %d", expectedTotal, prof.RootStack.Values[0])
	}

	t.Logf("Root values: %v", prof.RootStack.Values)
	t.Logf("Lines: %d", len(prof.Lines))
	t.Logf("Highest: %d", prof.HighestLines)
}

func TestParsePprof(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "pprof_data", "profile-10seconds.out"))
	if err != nil {
		t.Fatal(err)
	}

	prof, err := parser.Parse(data, "profile-10seconds.out")
	if err != nil {
		t.Fatal(err)
	}

	if prof.RootStack == nil {
		t.Fatal("root stack is nil")
	}

	t.Logf("Root values: %v", prof.RootStack.Values)
	t.Logf("Lines: %d", len(prof.Lines))
	t.Logf("Highest: %d", prof.HighestLines)
	t.Logf("Sample types: %v", prof.SampleTypes)
	t.Logf("Root children: %d", len(prof.RootStack.Children))
}

func TestParseGoroutinePprof(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "pprof_data", "goroutine.out"))
	if err != nil {
		t.Fatal(err)
	}

	prof, err := parser.Parse(data, "goroutine.out")
	if err != nil {
		t.Fatal(err)
	}

	if prof.RootStack == nil {
		t.Fatal("root stack is nil")
	}

	t.Logf("Root values: %v", prof.RootStack.Values)
	t.Logf("Lines: %d", len(prof.Lines))
	t.Logf("Highest: %d", prof.HighestLines)
	t.Logf("Sample types: %v", prof.SampleTypes)
}

func TestParseHeapPprof(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "pprof_data", "heap.out"))
	if err != nil {
		t.Fatal(err)
	}

	prof, err := parser.Parse(data, "heap.out")
	if err != nil {
		t.Fatal(err)
	}

	if prof.RootStack == nil {
		t.Fatal("root stack is nil")
	}

	t.Logf("Root values: %v", prof.RootStack.Values)
	t.Logf("Lines: %d", len(prof.Lines))
	t.Logf("Highest: %d", prof.HighestLines)
	t.Logf("Sample types: %v", prof.SampleTypes)
}

func TestParseStackcollapseSimple(t *testing.T) {
	data := []byte("a;b;c 1\na;b;c 1\na;b;d 4\na;b;c 3\na;b 5\n")
	prof, err := parser.Parse(data, "test.txt")
	if err != nil {
		t.Fatal(err)
	}

	// Total: 1+1+4+3+5 = 14
	if prof.RootStack.Values[0] != 14 {
		t.Errorf("expected 14, got %d", prof.RootStack.Values[0])
	}

	// Lines should be: root, a, b, c/d
	t.Logf("Lines count: %d", len(prof.Lines))
	for i, line := range prof.Lines {
		var names []string
		for _, f := range line {
			names = append(names, f.Name)
		}
		t.Logf("Line %d: %v", i, names)
	}
}
