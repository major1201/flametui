package parser

import (
	"fmt"

	"github.com/major1201/flametui/pkg/parser/pprof"
	"github.com/major1201/flametui/pkg/parser/stackcollapse"
	"github.com/major1201/flametui/pkg/profile"
)

// Parse auto-detects the format and parses the profile data.
func Parse(data []byte, filename string) (*profile.Profile, error) {
	parsers := []struct {
		name   string
		parser Parser
	}{
		{"stackcollapse", stackcollapse.NewParser(filename)},
		{"pprof", pprof.NewParser(filename)},
	}

	for _, p := range parsers {
		if p.parser.Validate(data) {
			prof, err := p.parser.Parse(data)
			if err != nil {
				return nil, fmt.Errorf("parser %s failed: %w", p.name, err)
			}
			return prof, nil
		}
	}

	return nil, fmt.Errorf("could not detect profile format for %s", filename)
}
