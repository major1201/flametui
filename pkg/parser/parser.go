package parser

import "github.com/major1201/flametui/pkg/profile"

// Parser defines the interface for profile parsers.
type Parser interface {
	Parse(data []byte) (*profile.Profile, error)
	Validate(data []byte) bool
}
