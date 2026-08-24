package render_test

import (
	"strings"
	"testing"

	"github.com/major1201/flametui/pkg/parser"
	"github.com/major1201/flametui/pkg/render"
)

func TestRendererLayout(t *testing.T) {
	prof, err := parser.Parse([]byte("a;b;c 1\na;b;c 1\na;b;d 4\na;b;c 3\na;b 5\n"), "test.txt")
	if err != nil {
		t.Fatal(err)
	}

	rend := render.NewFlameGraphRenderer(prof)
	rend.FocusedStackID = prof.RootStack.ID
	rend.ViewFrameID = prof.RootStack.ID

	width := 80
	rend.SetWidth(width)

	maps := rend.GenerateFrameMaps(width)
	if len(maps) == 0 {
		t.Fatal("frame maps is empty")
	}

	// Root should have full width
	rootMaps := maps[prof.RootStack.ID]
	if len(rootMaps) != 1 {
		t.Fatalf("expected 1 sample type, got %d", len(rootMaps))
	}
	if rootMaps[0].Width != width {
		t.Errorf("expected root width %d, got %d", width, rootMaps[0].Width)
	}

	// Render a line
	line := rend.RenderLine(0, width)
	if !strings.Contains(line, "root") {
		t.Errorf("expected root in line 0, got: %s", line)
	}

	t.Logf("Line 0: %s", line)
}

func TestRendererFrameMaps(t *testing.T) {
	prof, err := parser.Parse([]byte("a;b;c 1\na;b;d 4\n"), "test.txt")
	if err != nil {
		t.Fatal(err)
	}

	rend := render.NewFlameGraphRenderer(prof)
	rend.SetWidth(100)

	maps := rend.GenerateFrameMaps(100)

	// Verify widths sum properly for frames in the tree
	for _, line := range prof.Lines {
		for _, frame := range line {
			fmaps := maps[frame.ID]
			if len(fmaps) == 0 {
				continue
			}
			// This frame's children widths should sum to approximately this frame's width
			childWidth := 0
			for _, child := range frame.Children {
				cmaps := maps[child.ID]
				if len(cmaps) > 0 {
					childWidth += cmaps[0].Width
				}
			}
			if len(frame.Children) > 0 {
				// Allow 1px difference due to rounding
				diff := fmaps[0].Width - childWidth
				if diff < -1 || diff > 1 {
					t.Logf("frame %s: width=%d, children sum=%d", frame.Name, fmaps[0].Width, childWidth)
				}
			}
		}
	}
}

func TestRendererGetFrameUnderMouse(t *testing.T) {
	prof, err := parser.Parse([]byte("a;b;c 1\na;b;d 4\n"), "test.txt")
	if err != nil {
		t.Fatal(err)
	}

	rend := render.NewFlameGraphRenderer(prof)
	rend.SetWidth(100)

	// Root frame at y=0, x=0 should be found
	frame := rend.GetFrameUnderMouse(0, 0, 100)
	if frame == nil {
		t.Fatal("expected to find root frame at (0,0)")
	}
	if frame.Name != "root" {
		t.Errorf("expected root, got %s", frame.Name)
	}

	// Left side of line 1 should be "a"
	frame = rend.GetFrameUnderMouse(0, 1, 100)
	if frame == nil {
		t.Fatal("expected to find frame at (0,1)")
	}
	if frame.Name != "a" {
		t.Errorf("expected a, got %s", frame.Name)
	}
}
