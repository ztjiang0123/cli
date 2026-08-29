package cli

import (
	"strconv"
)

type (
	UintFlag   = FlagBase[uint, IntegerConfig, uintValue[uint]]
	Uint8Flag  = FlagBase[uint8, IntegerConfig, uintValue[uint8]]
	Uint16Flag = FlagBase[uint16, IntegerConfig, uintValue[uint16]]
	Uint32Flag = FlagBase[uint32, IntegerConfig, uintValue[uint32]]
	Uint64Flag = FlagBase[uint64, IntegerConfig, uintValue[uint64]]
)

// -- uint Value
type uintValue[T uint | uint8 | uint16 | uint32 | uint64] struct {
	val  *T
	base int
}

// Below functions are to satisfy the ValueCreator interface

func (i uintValue[T]) Create(val T, p *T, c IntegerConfig) Value {
	*p = val

	return &uintValue[T]{
		val:  p,
		base: c.Base,
	}
}

func (i uintValue[T]) ToString(b T) string {
	i.val = &b
	return i.String()
}

// Below functions are to satisfy the flag.Value interface

func (i *uintValue[T]) Set(s string) error {
	return setIntegerValue(i.val, i.base, s, strconv.ParseUint)
}

func (i *uintValue[T]) Get() any { return *i.val }

func (i *uintValue[T]) String() string {
	return formatIntegerValue(*i.val, i.base, strconv.FormatUint)
}

// Uint looks up the value of a local Uint64Flag, returns
// 0 if not found
func (cmd *Command) Uint(name string) uint {
	return getUint[uint](cmd, name)
}

// Uint8 looks up the value of a local Uint8Flag, returns
// 0 if not found
func (cmd *Command) Uint8(name string) uint8 {
	return getUint[uint8](cmd, name)
}

// Uint16 looks up the value of a local Uint16Flag, returns
// 0 if not found
func (cmd *Command) Uint16(name string) uint16 {
	return getUint[uint16](cmd, name)
}

// Uint32 looks up the value of a local Uint32Flag, returns
// 0 if not found
func (cmd *Command) Uint32(name string) uint32 {
	return getUint[uint32](cmd, name)
}

// Uint64 looks up the value of a local Uint64Flag, returns
// 0 if not found
func (cmd *Command) Uint64(name string) uint64 {
	return getUint[uint64](cmd, name)
}

func getUint[T uint | uint8 | uint16 | uint32 | uint64](cmd *Command, name string) T {
	if v, ok := cmd.Value(name).(T); ok {
		tracef("uint available for flag name %[1]q with value=%[2]v (cmd=%[3]q)", name, v, cmd.Name)

		return v
	}

	tracef("uint NOT available for flag name %[1]q (cmd=%[2]q)", name, cmd.Name)
	return 0
}
