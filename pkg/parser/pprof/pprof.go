package pprof

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/major1201/flametui/pkg/profile"
)

// Protobuf wire types
const (
	wireVarint = 0
	wire64bit  = 1
	wireBytes  = 2
	wire32bit  = 5
)

// Parser parses pprof (protobuf) format profiles.
type Parser struct {
	filename string
	nextID   int
	root     *profile.Frame
	highest  int
	idStore  map[int]*profile.Frame
}

// NewParser creates a new pprof parser.
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

// Parse parses pprof binary data.
func (p *Parser) Parse(data []byte) (*profile.Profile, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to decompress gzip: %w", err)
		}
		defer reader.Close()
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(reader); err != nil {
			return nil, fmt.Errorf("failed to read gzip: %w", err)
		}
		data = buf.Bytes()
	}

	pb, err := parseProfileProto(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pprof data: %w", err)
	}
	return p.buildProfile(pb)
}

func (p *Parser) buildProfile(pb *ProfileProto) (*profile.Profile, error) {
	st := pb.StringTable

	sampleTypes := make([]profile.SampleType, len(pb.SampleType))
	for i, vt := range pb.SampleType {
		sampleTypes[i] = profile.SampleType{
			Type: strFromST(st, vt.Type),
			Unit: strFromST(st, vt.Unit),
		}
	}

	// Resolve function names from string table
	for _, fn := range pb.Functions {
		fn.Name = strFromST(st, fn.NameIdx)
		fn.SystemName = strFromST(st, fn.SystemNameIdx)
		fn.Filename = strFromST(st, fn.FilenameIdx)
	}
	for _, m := range pb.Mappings {
		m.Filename = strFromST(st, m.FilenameIdx)
		m.BuildID = strFromST(st, m.BuildIDIdx)
	}
	for _, loc := range pb.Locations {
		if loc.MappingIdx > 0 && int(loc.MappingIdx) <= len(pb.Mappings) {
			loc.Mapping = pb.Mappings[loc.MappingIdx-1]
		}
	}

	p.root.Values = make([]int64, len(sampleTypes))

	for _, sample := range pb.Sample {
		childFrame := p.parseSample(sample, pb)
		if childFrame == nil {
			continue
		}
		for i := range p.root.Values {
			if i < len(childFrame.Values) {
				p.root.Values[i] += childFrame.Values[i]
			}
		}
		p.root.PileUp(childFrame)
	}

	prof := profile.NewProfile(
		p.filename,
		p.root,
		p.highest,
		len(pb.Sample),
		sampleTypes,
		p.idStore,
	)

	if pb.DefaultSampleType != 0 {
		prof.DefaultSampleTypeIndex = int(pb.DefaultSampleType)
	} else {
		// pprof spec: 0 means "use the last sample type"
		prof.DefaultSampleTypeIndex = -1
	}
	if pb.TimeNanos > 0 {
		t := time.Unix(0, pb.TimeNanos)
		prof.CreatedAt = t.Format(time.RFC3339)
	}
	prof.Period = pb.Period
	if pb.PeriodType != nil {
		prof.PeriodType = &profile.SampleType{
			Type: strFromST(st, pb.PeriodType.Type),
			Unit: strFromST(st, pb.PeriodType.Unit),
		}
	}

	return prof, nil
}

func strFromST(st []string, idx int64) string {
	if idx < 0 || int(idx) >= len(st) {
		return ""
	}
	return st[idx]
}

func (p *Parser) parseSample(sample *Sample, pb *ProfileProto) *profile.Frame {
	values := make([]int64, len(sample.Value))
	for i, v := range sample.Value {
		values[i] = v
	}

	// Reverse locations (deepest frame first)
	locations := make([]*Location, len(sample.LocationID))
	for i, locID := range sample.LocationID {
		if loc, ok := pb.Locations[int(locID)]; ok {
			locations[len(sample.LocationID)-1-i] = loc
		}
	}

	myDepth := 0
	for _, loc := range locations {
		if loc != nil {
			myDepth += len(loc.Lines)
		}
	}
	if myDepth > p.highest {
		p.highest = myDepth
	}

	var head, currentParent *profile.Frame
	for _, loc := range locations {
		if loc == nil {
			continue
		}
		for _, line := range loc.Lines {
			fn := pb.Functions[line.FunctionID]
			if fn == nil {
				continue
			}
			frame := p.lineToFrame(loc, line, fn, values)
			if currentParent != nil {
				frame.Parent = currentParent
				currentParent.Children = []*profile.Frame{frame}
			}
			if head == nil {
				head = frame
			}
			currentParent = frame
		}
	}
	return head
}

func (p *Parser) lineToFrame(loc *Location, line *Line, fn *Function, values []int64) *profile.Frame {
	frame := profile.NewFrame(fn.Name, p.idGenerator())
	frame.Root = p.root
	frame.Values = make([]int64, len(values))
	copy(frame.Values, values)

	frame.Line = &profile.Line{
		LineNo: line.LineNo,
		Function: &profile.Function{
			Name:       fn.Name,
			Filename:   fn.Filename,
			SystemName: fn.SystemName,
			StartLine:  fn.StartLine,
		},
	}

	if loc.Mapping != nil {
		frame.Mapping = &profile.Mapping{
			Filename: loc.Mapping.Filename,
			BuildID:  loc.Mapping.BuildID,
		}
	}

	// Color key: module name (first part of function name)
	// Display name: strip package path (e.g. github.com/prometheus/.../collector.NodeCollector.Collect.func1 -> NodeCollector.Collect.func1)
	parts := strings.Split(fn.Name, "/")
	funcName := parts[len(parts)-1]
	frame.SetDisplayName(funcName)
	frame.SetColorKey(funcName)
	if dotIdx := strings.IndexByte(funcName, '.'); dotIdx > 0 {
		frame.SetColorKey(funcName[:dotIdx])
	}

	p.idStore[frame.ID] = frame
	return frame
}

// Validate checks if the data looks like pprof format.
func (p *Parser) Validate(data []byte) bool {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return false
		}
		defer reader.Close()
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(reader); err != nil {
			return false
		}
		data = buf.Bytes()
	}
	pb, err := parseProfileProto(data)
	if err != nil {
		return false
	}
	// Must have at least sample types or samples to be valid pprof
	return len(pb.SampleType) > 0 || len(pb.Sample) > 0
}

// --- Protobuf type definitions ---

type ValueTypePB struct {
	Type int64
	Unit int64
}

type Sample struct {
	LocationID []uint64
	Value      []int64
}

type Mapping struct {
	ID              uint64
	MemoryStart     uint64
	MemoryLimit     uint64
	FileOffset      uint64
	FilenameIdx     int64
	BuildIDIdx      int64
	Filename        string
	BuildID         string
	HasFunctions    bool
	HasFilenames    bool
	HasLineNumbers  bool
	HasInlineFrames bool
}

type Location struct {
	ID         uint64
	MappingIdx uint64
	Mapping    *Mapping
	Address    uint64
	Lines      []*Line
	IsFolded   bool
}

type Line struct {
	FunctionID int
	LineNo     int64
}

type Function struct {
	ID            int
	NameIdx       int64
	SystemNameIdx int64
	FilenameIdx   int64
	Name          string
	SystemName    string
	Filename      string
	StartLine     int64
}

type ProfileProto struct {
	SampleType        []ValueTypePB
	Sample            []*Sample
	Mappings          []*Mapping
	Locations         map[int]*Location
	Functions         map[int]*Function
	StringTable       []string
	DropFrames        int64
	KeepFrames        int64
	TimeNanos         int64
	DurationNanos     int64
	PeriodType        *ValueTypePB
	Period            int64
	DefaultSampleType int64
}

// --- Protobuf parser ---

func parseProfileProto(data []byte) (*ProfileProto, error) {
	buf := bytes.NewReader(data)
	p := &ProfileProto{
		Locations: make(map[int]*Location),
		Functions: make(map[int]*Function),
	}

	for buf.Len() > 0 {
		tag, err := binary.ReadUvarint(buf)
		if err != nil {
			break
		}
		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x7)

		switch fieldNum {
		case 1: // sample_type
			vt, err := parseValueType(msgBytes(buf, wireType))
			if err != nil {
				return nil, err
			}
			p.SampleType = append(p.SampleType, *vt)
		case 2: // sample
			s, err := parseSample(msgBytes(buf, wireType))
			if err != nil {
				return nil, err
			}
			p.Sample = append(p.Sample, s)
		case 3: // mapping
			m, err := parseMapping(msgBytes(buf, wireType))
			if err != nil {
				return nil, err
			}
			p.Mappings = append(p.Mappings, m)
		case 4: // location
			loc, err := parseLocation(msgBytes(buf, wireType))
			if err != nil {
				return nil, err
			}
			p.Locations[int(loc.ID)] = loc
		case 5: // function
			fn, err := parseFunction(msgBytes(buf, wireType))
			if err != nil {
				return nil, err
			}
			p.Functions[fn.ID] = fn
		case 6: // string_table
			data := msgBytes(buf, wireType)
			p.StringTable = append(p.StringTable, string(data))
		case 7: // drop_frames
			p.DropFrames = readVarint(buf, wireType)
		case 8: // keep_frames
			p.KeepFrames = readVarint(buf, wireType)
		case 9: // time_nanos
			p.TimeNanos = readVarint(buf, wireType)
		case 10: // duration_nanos
			p.DurationNanos = readVarint(buf, wireType)
		case 11: // period_type
			vt, err := parseValueType(msgBytes(buf, wireType))
			if err != nil {
				return nil, err
			}
			p.PeriodType = vt
		case 12: // period
			p.Period = readVarint(buf, wireType)
		case 13: // comment
			readVarint(buf, wireType) // skip
		case 14: // default_sample_type
			p.DefaultSampleType = readVarint(buf, wireType)
		default:
			skip(buf, wireType)
		}
	}

	return p, nil
}

func msgBytes(r *bytes.Reader, wireType int) []byte {
	if wireType != wireBytes {
		skip(r, wireType)
		return nil
	}
	length, _ := binary.ReadUvarint(r)
	data := make([]byte, length)
	io.ReadFull(r, data)
	return data
}

func readVarint(r *bytes.Reader, wireType int) int64 {
	if wireType == wireVarint {
		v, _ := binary.ReadUvarint(r)
		return int64(v)
	}
	skip(r, wireType)
	return 0
}

func skip(r *bytes.Reader, wireType int) {
	switch wireType {
	case wireVarint:
		binary.ReadUvarint(r)
	case wire64bit:
		r.Seek(8, io.SeekCurrent)
	case wireBytes:
		length, _ := binary.ReadUvarint(r)
		r.Seek(int64(length), io.SeekCurrent)
	case wire32bit:
		r.Seek(4, io.SeekCurrent)
	}
}

func parseValueType(data []byte) (*ValueTypePB, error) {
	vt := &ValueTypePB{}
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, _ := binary.ReadUvarint(r)
		fn := int(tag >> 3)
		wt := int(tag & 0x7)
		switch fn {
		case 1:
			vt.Type = readVarint(r, wt)
		case 2:
			vt.Unit = readVarint(r, wt)
		default:
			skip(r, wt)
		}
	}
	return vt, nil
}

func parseSample(data []byte) (*Sample, error) {
	s := &Sample{}
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, _ := binary.ReadUvarint(r)
		fn := int(tag >> 3)
		wt := int(tag & 0x7)
		switch fn {
		case 1: // location_id
			if wt == wireBytes {
				packed := msgBytes(r, wt)
				pr := bytes.NewReader(packed)
				for pr.Len() > 0 {
					v, _ := binary.ReadUvarint(pr)
					s.LocationID = append(s.LocationID, v)
				}
			} else {
				v, _ := binary.ReadUvarint(r)
				s.LocationID = append(s.LocationID, v)
			}
		case 2: // value
			if wt == wireBytes {
				packed := msgBytes(r, wt)
				pr := bytes.NewReader(packed)
				for pr.Len() > 0 {
					v, _ := binary.ReadUvarint(pr)
					s.Value = append(s.Value, int64(v))
				}
			} else {
				v, _ := binary.ReadUvarint(r)
				s.Value = append(s.Value, int64(v))
			}
		case 3: // label
			skip(r, wt)
		default:
			skip(r, wt)
		}
	}
	return s, nil
}

func parseMapping(data []byte) (*Mapping, error) {
	m := &Mapping{}
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, _ := binary.ReadUvarint(r)
		fn := int(tag >> 3)
		wt := int(tag & 0x7)
		switch fn {
		case 1:
			m.ID, _ = binary.ReadUvarint(r)
		case 2:
			m.MemoryStart, _ = binary.ReadUvarint(r)
		case 3:
			m.MemoryLimit, _ = binary.ReadUvarint(r)
		case 4:
			m.FileOffset, _ = binary.ReadUvarint(r)
		case 5:
			m.FilenameIdx = readVarint(r, wt)
		case 6:
			m.BuildIDIdx = readVarint(r, wt)
		case 7:
			m.HasFunctions = readVarint(r, wt) != 0
		case 8:
			m.HasFilenames = readVarint(r, wt) != 0
		case 9:
			m.HasLineNumbers = readVarint(r, wt) != 0
		case 10:
			m.HasInlineFrames = readVarint(r, wt) != 0
		default:
			skip(r, wt)
		}
	}
	return m, nil
}

func parseLocation(data []byte) (*Location, error) {
	loc := &Location{}
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, _ := binary.ReadUvarint(r)
		fn := int(tag >> 3)
		wt := int(tag & 0x7)
		switch fn {
		case 1:
			loc.ID, _ = binary.ReadUvarint(r)
		case 2:
			loc.MappingIdx, _ = binary.ReadUvarint(r)
		case 3:
			loc.Address, _ = binary.ReadUvarint(r)
		case 4: // line
			lineData := msgBytes(r, wt)
			line, err := parseLine(lineData)
			if err != nil {
				return nil, err
			}
			loc.Lines = append(loc.Lines, line)
		case 5:
			loc.IsFolded = readVarint(r, wt) != 0
		default:
			skip(r, wt)
		}
	}
	return loc, nil
}

func parseLine(data []byte) (*Line, error) {
	l := &Line{}
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, _ := binary.ReadUvarint(r)
		fn := int(tag >> 3)
		wt := int(tag & 0x7)
		switch fn {
		case 1:
			v, _ := binary.ReadUvarint(r)
			l.FunctionID = int(v)
		case 2:
			l.LineNo = readVarint(r, wt)
		default:
			skip(r, wt)
		}
	}
	return l, nil
}

func parseFunction(data []byte) (*Function, error) {
	f := &Function{}
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, _ := binary.ReadUvarint(r)
		fn := int(tag >> 3)
		wt := int(tag & 0x7)
		switch fn {
		case 1:
			v, _ := binary.ReadUvarint(r)
			f.ID = int(v)
		case 2:
			f.NameIdx = readVarint(r, wt)
		case 3:
			f.SystemNameIdx = readVarint(r, wt)
		case 4:
			f.FilenameIdx = readVarint(r, wt)
		case 5:
			f.StartLine = readVarint(r, wt)
		default:
			skip(r, wt)
		}
	}
	return f, nil
}
