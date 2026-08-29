package cli

// integerValueBase holds the state shared by the signed (intValue) and
// unsigned (uintValue) integer flag value types. Both embed it so their
// otherwise identical ValueCreator plumbing can be shared.
type integerValueBase[T anyInteger] struct {
	val  *T
	base int
}

// newIntegerValueBase stores val into p and returns a base configured with the
// destination pointer and the requested formatting base. It is the shared body
// of intValue.Create and uintValue.Create.
func newIntegerValueBase[T anyInteger](val T, p *T, c IntegerConfig) integerValueBase[T] {
	*p = val

	return integerValueBase[T]{
		val:  p,
		base: c.Base,
	}
}

func (i integerValueBase[T]) Get() any { return *i.val }
