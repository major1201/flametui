package stackcollapse

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/major1201/flametui/pkg/profile"
)

var lineRegex = regexp.MustCompile(`^(.*?)\s+(\d+)$`)

// Parser parses stackcollapse (Brendan Gregg's flamegraph) format.
type Parser struct {
	filename string
	nextID   int
	root     *profile.Frame
	highest  int
	idStore  map[int]*profile.Frame
}

// NewParser creates a new stackcollapse parser.
func NewParser(filename string) *Parser {
	root := profile.NewFrame("root", 0)
	root.Root = root
	return &Parser{
		filename: filename,
		root:     root,
		nextID:   1, // 0 is reserved for root
		idStore:  map[int]*profile.Frame{0: root},
	}
}

func (p *Parser) idGenerator() int {
	id := p.nextID
	p.nextID++
	return id
}

// Parse parses stackcollapse format data.
func (p *Parser) Parse(data []byte) (*profile.Profile, error) {
	text := string(data)
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		p.parseLine(line)
	}

	// Ensure root has values for each sample type
	if len(p.root.Values) == 0 {
		p.root.Values = []int64{0}
	}

	return profile.NewProfile(
		p.filename,
		p.root,
		p.highest,
		len(lines),
		[]profile.SampleType{{Type: "samples", Unit: "count"}},
		p.idStore,
	), nil
}

func (p *Parser) parseLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "#") {
		return
	}

	matches := lineRegex.FindStringSubmatch(line)
	if matches == nil {
		return
	}

	frameStr := matches[1]
	count, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return
	}

	frameNames := strings.Split(frameStr, ";")

	var head, prev *profile.Frame
	for _, name := range frameNames {
		frame := profile.NewFrame(name, p.idGenerator())
		frame.Root = p.root
		frame.Values = []int64{count}

		p.idStore[frame.ID] = frame

		if prev != nil {
			prev.Children = []*profile.Frame{frame}
			frame.Parent = prev
		}
		if head == nil {
			head = frame
		}
		prev = frame
	}

	if head != nil {
		p.root.PileUp(head)
		if len(p.root.Values) == 0 {
			p.root.Values = []int64{head.Values[0]}
		} else {
			p.root.Values[0] += head.Values[0]
		}
	}

	if len(frameNames) > p.highest {
		p.highest = len(frameNames)
	}
}

// Validate checks if the data looks like stackcollapse format.
func (p *Parser) Validate(data []byte) bool {
	text := string(data)
	lines := strings.Split(text, "\n")

	validCount := 0
	checked := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		checked++
		if checked > 100 {
			break
		}
		if lineRegex.MatchString(line) {
			validCount++
		}
	}

	if checked == 0 {
		return false
	}
	return validCount > 0 && float64(validCount)/float64(checked) > 0.5
}
