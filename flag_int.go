package cli

import (
	"strconv"
)

type (
	IntFlag   = FlagBase[int, IntegerConfig, intValue[int]]
	Int8Flag  = FlagBase[int8, IntegerConfig, intValue[int8]]
	Int16Flag = FlagBase[int16, IntegerConfig, intValue[int16]]
	Int32Flag = FlagBase[int32, IntegerConfig, intValue[int32]]
	Int64Flag = FlagBase[int64, IntegerConfig, intValue[int64]]
)

// IntegerConfig is the configuration for all integer type flags
type IntegerConfig struct {
	Base int
}

// -- int Value
type intValue[T int | int8 | int16 | int32 | int64] struct {
	integerValueBase[T]
}

// Below functions are to satisfy the ValueCreator interface

func (i intValue[T]) Create(val T, p *T, c IntegerConfig) Value {
	return &intValue[T]{newIntegerValueBase(val, p, c)}
}

func (i intValue[T]) ToString(b T) string {
	i.val = &b
	return i.String()
}

// Below functions are to satisfy the flag.Value interface

func (i *intValue[T]) Set(s string) error {
	return setIntegerValue(i.val, i.base, s, strconv.ParseInt)
}

func (i *intValue[T]) String() string {
	return formatIntegerValue(*i.val, i.base, strconv.FormatInt)
}

// Int looks up the value of a local Int64Flag, returns
// 0 if not found
func (cmd *Command) Int(name string) int {
	return getInt[int](cmd, name)
}

// Int8 looks up the value of a local Int8Flag, returns
// 0 if not found
func (cmd *Command) Int8(name string) int8 {
	return getInt[int8](cmd, name)
}

// Int16 looks up the value of a local Int16Flag, returns
// 0 if not found
func (cmd *Command) Int16(name string) int16 {
	return getInt[int16](cmd, name)
}

// Int32 looks up the value of a local Int32Flag, returns
// 0 if not found
func (cmd *Command) Int32(name string) int32 {
	return getInt[int32](cmd, name)
}

// Int64 looks up the value of a local Int64Flag, returns
// 0 if not found
func (cmd *Command) Int64(name string) int64 {
	return getInt[int64](cmd, name)
}

func getInt[T int | int8 | int16 | int32 | int64](cmd *Command, name string) T {
	return getFlagValue[T](cmd, name)
}
