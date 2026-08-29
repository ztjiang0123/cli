package cli

import (
	"fmt"
	"strings"
)

type StringFlag = FlagBase[string, StringConfig, stringValue]

// StringConfig defines the configuration for string flags
type StringConfig struct {
	// Whether to trim whitespace of parsed value
	TrimSpace bool
}

// -- string Value
type stringValue struct {
	destination *string
	trimSpace   bool
}

// Below functions are to satisfy the ValueCreator interface

func (s stringValue) Create(val string, p *string, c StringConfig) Value {
	*p = val
	return &stringValue{
		destination: p,
		trimSpace:   c.TrimSpace,
	}
}

func (s stringValue) ToString(val string) string {
	s.destination = &val
	return s.String()
}

// Below functions are to satisfy the flag.Value interface

func (s *stringValue) Set(val string) error {
	if s.trimSpace {
		val = strings.TrimSpace(val)
	}
	*s.destination = val
	return nil
}

func (s *stringValue) Get() any { return *s.destination }

func (s *stringValue) String() string {
	if s.destination != nil && *s.destination != "" {
		return fmt.Sprintf("%q", *s.destination)
	}
	return ""
}

func (cmd *Command) String(name string) string {
	return getFlagValue[string](cmd, name)
}
