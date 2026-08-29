package cli

type GenericFlag = FlagBase[Value, NoConfig, genericValue]

// -- Value Value
type genericValue struct {
	val Value
}

// Below functions are to satisfy the ValueCreator interface

func (f genericValue) Create(val Value, p *Value, c NoConfig) Value {
	*p = val
	return &genericValue{
		val: *p,
	}
}

func (f genericValue) ToString(b Value) string {
	f.val = b
	return f.String()
}

// Below functions are to satisfy the flag.Value interface

func (f *genericValue) Set(s string) error {
	if f.val != nil {
		return f.val.Set(s)
	}
	return nil
}

func (f *genericValue) Get() any {
	if f.val != nil {
		return f.val.Get()
	}
	return nil
}

func (f *genericValue) String() string {
	if f.val != nil {
		return f.val.String()
	}
	return ""
}

func (f *genericValue) IsBoolFlag() bool {
	if f.val == nil {
		return false
	}
	bf, ok := f.val.(boolFlag)
	return ok && bf.IsBoolFlag()
}

// Generic looks up the value of a local GenericFlag, returns
// nil if not found
func (cmd *Command) Generic(name string) Value {
	return getFlagValue[Value](cmd, name)
}
