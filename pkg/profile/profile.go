package profile

import (
	"fmt"
	"sync"
)

// SampleType represents a type of sample with its unit.
type SampleType struct {
	Type string
	Unit string
}

// Frame represents a node in the flamegraph tree.
type Frame struct {
	ID       int
	Name     string
	Parent   *Frame
	Children []*Frame
	Values   []int64
	Root     *Frame

	// pprof-specific fields
	Line    *Line
	Mapping *Mapping

	// Color key for consistent coloring
	colorKey string
	// Display name (may differ from Name, e.g. strip package path)
	displayName string
}

// Line represents source line info from pprof.
type Line struct {
	LineNo   int64
	Function *Function
}

// Function represents a function from pprof.
type Function struct {
	Name       string
	Filename   string
	SystemName string
	StartLine  int64
}

// Mapping represents a memory mapping from pprof.
type Mapping struct {
	Filename string
	BuildID  string
}

// Profile holds the parsed profile data.
type Profile struct {
	Filename               string
	RootStack              *Frame
	HighestLines           int
	TotalSample            int
	SampleTypes            []SampleType
	IDStore                map[int]*Frame
	DefaultSampleTypeIndex int

	Period     int64
	PeriodType *SampleType
	CreatedAt  string

	Lines           [][]*Frame
	FrameIDToLineNo map[int]int
	NameAggr        map[string][]*Frame

	mu sync.RWMutex
}

// NewProfile creates a new profile.
func NewProfile(filename string, root *Frame, highestLines int, totalSamples int, sampleTypes []SampleType, idStore map[int]*Frame) *Profile {
	p := &Profile{
		Filename:               filename,
		RootStack:              root,
		HighestLines:           highestLines,
		TotalSample:            totalSamples,
		SampleTypes:            sampleTypes,
		IDStore:                idStore,
		DefaultSampleTypeIndex: 0,
	}
	p.Refresh()
	return p
}

// Refresh rebuilds the line-based layout from the frame tree.
func (p *Profile) Refresh() {
	root := p.RootStack

	lines := [][]*Frame{{root}}
	frameIDToLineNo := map[int]int{root.ID: 0}
	current := root.Children
	lineNo := 1

	for len(current) > 0 {
		line := make([]*Frame, 0, len(current))
		var nextLine []*Frame

		for _, child := range current {
			line = append(line, child)
			frameIDToLineNo[child.ID] = lineNo
			nextLine = append(nextLine, child.Children...)
		}

		lines = append(lines, line)
		lineNo++
		current = nextLine
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.Lines = lines
	p.FrameIDToLineNo = frameIDToLineNo
	p.NameAggr = p.buildNameAggr(root)
}

func (p *Profile) buildNameAggr(frame *Frame) map[string][]*Frame {
	result := make(map[string][]*Frame)
	result[frame.Name] = []*Frame{frame}

	for _, child := range frame.Children {
		childAggr := buildNameAggrRecursive(child, map[string]struct{}{frame.Name: {}})
		for k, v := range childAggr {
			result[k] = append(result[k], v...)
		}
	}
	return result
}

func buildNameAggrRecursive(frame *Frame, seen map[string]struct{}) map[string][]*Frame {
	result := make(map[string][]*Frame)
	name := frame.Name
	if _, ok := seen[name]; !ok {
		seen[name] = struct{}{}
		result[name] = []*Frame{frame}
	}
	for _, child := range frame.Children {
		// Copy seen set for each child (like Python's names | set([name]))
		childSeen := make(map[string]struct{}, len(seen)+1)
		for k := range seen {
			childSeen[k] = struct{}{}
		}
		childSeen[name] = struct{}{}
		childAggr := buildNameAggrRecursive(child, childSeen)
		for k, v := range childAggr {
			result[k] = append(result[k], v...)
		}
	}
	return result
}

// NewFrame creates a new frame.
func NewFrame(name string, id int) *Frame {
	return &Frame{
		ID:       id,
		Name:     name,
		Children: []*Frame{},
		Values:   []int64{},
		colorKey: name,
	}
}

// PileUp adds a child stack to this frame, merging values.
func (f *Frame) PileUp(child *Frame) {
	child.Parent = f

	for _, existChild := range f.Children {
		if existChild.Name == child.Name {
			// Merge values
			for i := range existChild.Values {
				if i < len(child.Values) {
					existChild.Values[i] += child.Values[i]
				}
			}
			for _, newChild := range child.Children {
				existChild.PileUp(newChild)
			}
			return
		}
	}

	f.Children = append(f.Children, child)
}

// ColorKey returns the key used for color assignment.
func (f *Frame) ColorKey() string {
	if f.colorKey != "" {
		return f.colorKey
	}
	return f.Name
}

// SetColorKey sets the color key for this frame.
func (f *Frame) SetColorKey(key string) {
	f.colorKey = key
}

// DisplayName returns the name to display on the flamegraph.
func (f *Frame) DisplayName() string {
	if f.displayName != "" {
		return f.displayName
	}
	return f.Name
}

// SetDisplayName sets the display name for this frame.
func (f *Frame) SetDisplayName(name string) {
	f.displayName = name
}

// Title returns the full name for display in the detail panel.
func (f *Frame) Title() string {
	return f.Name
}

// RenderDetail renders the stack detail for this frame.
func (f *Frame) RenderDetail(sampleIndex int, sampleUnit string) []string {
	var detail []string
	frame := f
	for frame != nil {
		lines := frame.renderOneFrameDetail(frame, sampleIndex, sampleUnit)
		detail = append(detail, lines...)
		frame = frame.Parent
	}
	return detail
}

func (f *Frame) renderOneFrameDetail(frame *Frame, sampleIndex int, sampleUnit string) []string {
	var result []string
	if frame.ID == 0 { // root
		total := int64(0)
		for _, c := range frame.Children {
			if sampleIndex < len(c.Values) {
				total += c.Values[sampleIndex]
			}
		}
		value := humanize(sampleUnit, total)
		binaryName := "root"
		if len(frame.Children) > 0 && frame.Children[0].Mapping != nil {
			binaryName = fmt.Sprintf("Binary: %s", frame.Children[0].Mapping.Filename)
		}
		result = append(result, fmt.Sprintf("%s %s", binaryName, value))
		return result
	}

	value := int64(0)
	if sampleIndex < len(frame.Values) {
		value = frame.Values[sampleIndex]
	}
	valueStr := humanize(sampleUnit, value)

	if frame.Line != nil && frame.Line.Function != nil {
		result = append(result, fmt.Sprintf("%s: %s", frame.Line.Function.Name, valueStr))
		result = append(result, fmt.Sprintf("  %s, line %d", frame.Line.Function.Filename, frame.Line.LineNo))
	} else {
		result = append(result, fmt.Sprintf("%s: %s", frame.Name, valueStr))
	}
	return result
}

func humanize(unit string, value int64) string {
	if unit == "bytes" {
		return sizeof(value)
	}
	return fmt.Sprintf("%d", value)
}

func sizeof(num int64) string {
	const unit = 1024
	if num < unit {
		return fmt.Sprintf("%d B", num)
	}
	div, exp := int64(unit), 0
	for n := num / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(num)/float64(div), "KMGTPE"[exp])
}
