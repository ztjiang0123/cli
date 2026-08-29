package cli

import "unsafe"

// anyInteger constrains the concrete integer flag types (int, uint and their
// sized variants).
type anyInteger interface {
	int | int8 | int16 | int32 | int64 | uint | uint8 | uint16 | uint32 | uint64
}

// setIntegerValue parses s using parse (either strconv.ParseInt or
// strconv.ParseUint) with the given base and the bit size of T, storing the
// converted result into *val. It is shared by the signed and unsigned integer
// flag values, which differ only in the strconv function they call.
func setIntegerValue[T anyInteger, W int64 | uint64](
	val *T,
	base int,
	s string,
	parse func(s string, base, bitSize int) (W, error),
) error {
	v, err := parse(s, base, int(unsafe.Sizeof(T(0))*8))
	if err != nil {
		return err
	}
	*val = T(v)
	return nil
}

// formatIntegerValue renders v using format (either strconv.FormatInt or
// strconv.FormatUint), defaulting an unset base (0) to 10. It is shared by the
// signed and unsigned integer flag values.
func formatIntegerValue[T anyInteger, W int64 | uint64](
	v T,
	base int,
	format func(w W, base int) string,
) string {
	if base == 0 {
		base = 10
	}

	return format(W(v), base)
}
