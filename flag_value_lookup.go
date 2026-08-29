package cli

// getFlagValue looks up the value of a local flag by name and returns it as
// type T. If the flag is not set or is of a different type, the zero value of
// T is returned. This is the shared implementation used by the typed accessors
// (Int, Uint, Float, Duration, Timestamp, ...).
func getFlagValue[T any](cmd *Command, name string) T {
	if v, ok := cmd.Value(name).(T); ok {
		tracef("%T available for flag name %[2]q with value=%[3]v (cmd=%[4]q)", v, name, v, cmd.Name)
		return v
	}

	var zero T
	tracef("%T NOT available for flag name %[2]q (cmd=%[3]q)", zero, name, cmd.Name)
	return zero
}
