package render

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/major1201/flametui/pkg/profile"
)

// FrameMap holds the computed position for a frame.
type FrameMap struct {
	Offset int
	Width  int
}

// ColorPalette manages color assignment for frames.
type ColorPalette struct {
	mu            sync.Mutex
	assignedColor map[string]string
}

// NewColorPalette creates a new color palette.
func NewColorPalette() *ColorPalette {
	return &ColorPalette{
		assignedColor: make(map[string]string),
	}
}

// GetColor returns an ANSI color for the given key.
func (p *ColorPalette) GetColor(key string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if color, ok := p.assignedColor[key]; ok {
		return color
	}

	// Warm colors: red-heavy, green-medium, blue-low
	r := 205 + rand.Intn(50)
	g := rand.Intn(230)
	b := rand.Intn(55)

	color := fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
	p.assignedColor[key] = color
	return color
}

// FlameGraphRenderer handles layout computation and rendering of the flamegraph.
type FlameGraphRenderer struct {
	Profile     *profile.Profile
	Palette     *ColorPalette
	SampleIndex int

	FocusedStackID int
	ViewFrameID    int

	mu                   sync.RWMutex
	frameMaps            map[int][]FrameMap
	width                int
	cachedFocusedStackID int
}

// NewFlameGraphRenderer creates a new renderer.
func NewFlameGraphRenderer(prof *profile.Profile) *FlameGraphRenderer {
	return &FlameGraphRenderer{
		Profile:        prof,
		Palette:        NewColorPalette(),
		FocusedStackID: prof.RootStack.ID,
		ViewFrameID:    prof.RootStack.ID,
	}
}

// SetWidth sets the rendering width and invalidates cache.
func (r *FlameGraphRenderer) SetWidth(width int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.width != width {
		r.width = width
		r.frameMaps = nil
	}
}

const reset = "\033[0m"

// GenerateFrameMaps computes the layout for all frames.
func (r *FlameGraphRenderer) GenerateFrameMaps(width int) map[int][]FrameMap {
	r.mu.RLock()
	if r.frameMaps != nil && r.width == width && r.cachedFocusedStackID == r.FocusedStackID {
		maps := r.frameMaps
		r.mu.RUnlock()
		return maps
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frameMaps != nil && r.width == width && r.cachedFocusedStackID == r.FocusedStackID {
		return r.frameMaps
	}

	r.width = width
	r.cachedFocusedStackID = r.FocusedStackID
	frameMaps := make(map[int][]FrameMap)

	focusedStack := r.Profile.IDStore[r.FocusedStackID]
	if focusedStack == nil {
		focusedStack = r.Profile.RootStack
	}
	stCount := len(focusedStack.Values)

	me := focusedStack
	for me != nil {
		frameMaps[me.ID] = make([]FrameMap, stCount)
		for i := range stCount {
			frameMaps[me.ID][i] = FrameMap{Offset: 0, Width: width}
		}
		me = me.Parent
	}

	generateChildrenMaps(focusedStack, frameMaps, width, stCount)
	r.frameMaps = frameMaps
	return frameMaps
}

func generateChildrenMaps(frame *profile.Frame, frameMaps map[int][]FrameMap, parentWidth int, stCount int) {
	myMaps, ok := frameMaps[frame.ID]
	if !ok || len(myMaps) < stCount {
		return
	}
	visibleChildren := make(map[int]bool)

	for sampleI := 0; sampleI < stCount && sampleI < len(myMaps); sampleI++ {
		myMap := myMaps[sampleI]
		if myMap.Width <= 0 {
			continue
		}

		var childWidths []float64
		for _, child := range frame.Children {
			childVal := int64(0)
			if sampleI < len(child.Values) {
				childVal = child.Values[sampleI]
			}
			parentVal := int64(0)
			if sampleI < len(frame.Values) {
				parentVal = frame.Values[sampleI]
			}
			if parentVal <= 0 {
				childWidths = append(childWidths, 0)
			} else {
				childWidths = append(childWidths, float64(childVal)/float64(parentVal)*float64(myMap.Width))
			}
		}

		rounded := saferound(childWidths, myMap.Width)

		offset := myMap.Offset
		for idx, child := range frame.Children {
			childWidth := rounded[idx]
			if _, ok := frameMaps[child.ID]; !ok {
				frameMaps[child.ID] = make([]FrameMap, stCount)
			}
			frameMaps[child.ID][sampleI] = FrameMap{
				Offset: offset,
				Width:  childWidth,
			}
			if childWidth > 0 {
				visibleChildren[child.ID] = true
			}
			offset += childWidth
		}
	}

	for _, child := range frame.Children {
		if visibleChildren[child.ID] {
			generateChildrenMaps(child, frameMaps, parentWidth, stCount)
		}
	}
}

func saferound(widths []float64, total int) []int {
	if len(widths) == 0 {
		return nil
	}
	result := make([]int, len(widths))
	floorSum := 0
	remainders := make([]float64, len(widths))

	for i, w := range widths {
		floored := int(math.Floor(w))
		result[i] = floored
		floorSum += floored
		remainders[i] = w - float64(floored)
	}

	remaining := total - floorSum
	if remaining > 0 {
		type idxRem struct {
			idx int
			rem float64
		}
		items := make([]idxRem, len(widths))
		for i, r := range remainders {
			items[i] = idxRem{i, r}
		}
		for i := range items {
			for j := i + 1; j < len(items); j++ {
				if items[j].rem > items[i].rem {
					items[i], items[j] = items[j], items[i]
				}
			}
		}
		for i := 0; i < remaining && i < len(items); i++ {
			result[items[i].idx]++
		}
	}

	return result
}

// RenderLine renders a single line of the flamegraph.
func (r *FlameGraphRenderer) RenderLine(y int, width int) (s string) {
	defer func() {
		if rec := recover(); rec != nil {
			s = fmt.Sprintf("PANIC(y=%d,width=%d,focusedStack=%d,sampleIdx=%d): %v", y, width, r.FocusedStackID, r.SampleIndex, rec)
		}
	}()

	prof := r.Profile
	if y >= len(prof.Lines) {
		return ""
	}

	line := prof.Lines[y]
	frameMaps := r.GenerateFrameMaps(width)

	var sb strings.Builder
	cursor := 0

	for _, frame := range line {
		fmaps, ok := frameMaps[frame.ID]
		if !ok {
			continue
		}
		if r.SampleIndex < 0 || r.SampleIndex >= len(fmaps) {
			continue
		}
		fmap := fmaps[r.SampleIndex]
		if fmap.Width <= 0 {
			continue
		}

		prePad := fmap.Offset - cursor
		if prePad > 0 {
			sb.WriteString(strings.Repeat(" ", prePad))
			cursor += prePad
		}

		bgColor := r.Palette.GetColor(frame.ColorKey())
		text := "▏" + frame.DisplayName()

		textRunes := utf8.RuneCountInString(text)
		if textRunes > fmap.Width {
			runes := []rune(text)
			text = string(runes[:fmap.Width])
		} else if textRunes < fmap.Width {
			text += strings.Repeat(" ", fmap.Width-textRunes)
		}

		// Blend ancestors with darker background
		expandBeforeLine := prof.FrameIDToLineNo[r.FocusedStackID]
		if y <= expandBeforeLine {
			// Darken the background
			bgColor = r.darkenColor(bgColor)
		}

		// Highlight selected frame
		if frame.ID == r.ViewFrameID {
			// White bg, bold black text
			sb.WriteString("\033[47;1;30m")
			sb.WriteString(text)
			sb.WriteString(reset)
		} else if r.ViewFrameID != 0 && frame.Name == r.Profile.IDStore[r.ViewFrameID].Name {
			// Purple-ish bg for other instances of same name
			sb.WriteString("\033[48;2;136;132;255;38;2;255;255;255m")
			sb.WriteString(text)
			sb.WriteString(reset)
		} else {
			sb.WriteString(bgColor)
			sb.WriteString(contrastTextColor(bgColor))
			sb.WriteString(text)
			sb.WriteString(reset)
		}

		cursor += fmap.Width
	}

	return sb.String()
}

func (r *FlameGraphRenderer) darkenColor(ansi string) string {
	// Blend with dark red (#8b0000) at 50% factor, matching Python's approach
	var rr, gg, bb int
	if _, err := fmt.Sscanf(ansi, "\033[48;2;%d;%d;%dm", &rr, &gg, &bb); err == nil {
		// Blend 50% with #8b0000 (139, 0, 0)
		rr = (rr + 139) / 2
		gg = gg / 2
		bb = bb / 2
		return fmt.Sprintf("\033[48;2;%d;%d;%dm", rr, gg, bb)
	}
	return ansi
}

// contrastTextColor returns the ANSI foreground color that contrasts with the given background.
// Uses ITU-R BT.601 luminance to decide between white (#fff) and black (#000) text.
func contrastTextColor(bgAnsi string) string {
	var r, g, b int
	if _, err := fmt.Sscanf(bgAnsi, "\033[48;2;%d;%d;%dm", &r, &g, &b); err == nil {
		luminance := float64(r)*0.299 + float64(g)*0.587 + float64(b)*0.114
		if luminance > 128 {
			return "\033[38;2;0;0;0m" // black text for light backgrounds
		}
	}
	return "\033[38;2;255;255;255m" // white text for dark backgrounds
}

// GetFrameUnderMouse returns the frame at the given position.
func (r *FlameGraphRenderer) GetFrameUnderMouse(x, y, width int) *profile.Frame {
	prof := r.Profile
	if y >= len(prof.Lines) {
		return nil
	}

	line := prof.Lines[y]
	frameMaps := r.GenerateFrameMaps(width)

	for _, frame := range line {
		fmaps, ok := frameMaps[frame.ID]
		if !ok {
			continue
		}
		if r.SampleIndex >= len(fmaps) {
			continue
		}
		fmap := fmaps[r.SampleIndex]
		if fmap.Offset <= x && x < fmap.Offset+fmap.Width {
			return frame
		}
	}
	return nil
}
