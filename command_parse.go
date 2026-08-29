package cli

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	providedButNotDefinedErrMsg = "flag provided but not defined: -"
	argumentNotProvidedErrMsg   = "flag needs an argument: "
)

// flagFromError tries to parse a provided flag from an error message. If the
// parsing fails, it returns the input error and an empty string
func flagFromError(err error) (string, error) {
	errStr := err.Error()
	trimmed := strings.TrimPrefix(errStr, providedButNotDefinedErrMsg)
	if errStr == trimmed {
		return "", err
	}
	return trimmed, nil
}

// parseAction tells the parseFlags loop how to proceed after handling a single
// leading argument.
type parseAction int

const (
	// parseContinue advances to the next argument.
	parseContinue parseAction = iota
	// parseBreak stops the loop and returns the accumulated positional args.
	parseBreak
	// parseStop returns immediately with the current result and error.
	parseStop
)

// applyPersistentFlags walks the command lineage and appends any ancestor
// persistent flags that are not shadowed by a local flag on cmd.
func (cmd *Command) applyPersistentFlags() {
	tracef("walking command lineage for persistent flags (cmd=%[1]q)", cmd.Name)

	for pCmd := cmd.parent; pCmd != nil; pCmd = pCmd.parent {
		tracef(
			"checking ancestor command=%[1]q for persistent flags (cmd=%[2]q)",
			pCmd.Name, cmd.Name,
		)

		for _, fl := range pCmd.allFlags() {
			flNames := fl.Names()

			pfl, ok := fl.(LocalFlag)
			if !ok || pfl.IsLocal() {
				tracef("skipping non-persistent flag %[1]q (cmd=%[2]q)", flNames, cmd.Name)
				continue
			}

			tracef(
				"checking for applying persistent flag=%[1]q pCmd=%[2]q (cmd=%[3]q)",
				flNames, pCmd.Name, cmd.Name,
			)

			if cmd.flagShadowedByLocal(flNames) {
				tracef("not applying as persistent flag=%[1]q (cmd=%[2]q)", flNames, cmd.Name)
				continue
			}

			tracef("applying as persistent flag=%[1]q (cmd=%[2]q)", flNames, cmd.Name)

			tracef("appending to applied flags flag=%[1]q (cmd=%[2]q)", flNames, cmd.Name)
			cmd.appliedFlags = append(cmd.appliedFlags, fl)
		}
	}
}

// flagShadowedByLocal reports whether any of the given flag names is already
// defined as a local flag on cmd.
func (cmd *Command) flagShadowedByLocal(names []string) bool {
	for _, name := range names {
		if cmd.lFlag(name) != nil {
			return true
		}
	}
	return false
}

// parseState holds the mutable slices threaded through flag parsing: rargs is
// the remaining unparsed arguments (its head is the argument being processed),
// and posArgs accumulates the positional arguments discovered so far.
type parseState struct {
	rargs   []string
	posArgs []string
}

// first returns the argument currently being processed.
func (st *parseState) first() string { return st.rargs[0] }

// remaining reports how many unparsed arguments are left (including the one
// currently being processed).
func (st *parseState) remaining() int { return len(st.rargs) }

// push appends a single argument to the positional args.
func (st *parseState) push(arg string) { st.posArgs = append(st.posArgs, arg) }

// pushRest appends every remaining unparsed argument to the positional args.
func (st *parseState) pushRest() { st.posArgs = append(st.posArgs, st.rargs...) }

// pushRestAfterFirst appends the remaining unparsed arguments except the one
// currently being processed to the positional args.
func (st *parseState) pushRestAfterFirst() { st.posArgs = append(st.posArgs, st.rargs[1:]...) }

// takeNextValue consumes the argument following the current one, returning it
// and advancing past it so the parse loop does not treat it as a flag.
func (st *parseState) takeNextValue() string {
	val := st.rargs[1]
	st.rargs = st.rargs[1:]
	return val
}

// flagArg is a flag argument that has been split into its name and, when
// present, the value supplied inline via "=".
type flagArg struct {
	raw          string // the original argument, including leading minuses
	name         string
	val          string
	valFromEqual bool
}

func (cmd *Command) parseFlags(args Args) (Args, error) {
	tracef("parsing flags from arguments %[1]q (cmd=%[2]q)", args, cmd.Name)

	cmd.setFlags = map[Flag]struct{}{}
	cmd.appliedFlags = cmd.allFlags()

	cmd.applyPersistentFlags()

	tracef("parsing flags iteratively tail=%[1]q (cmd=%[2]q)", args.Tail(), cmd.Name)
	defer tracef("done parsing flags (cmd=%[1]q)", cmd.Name)

	st := &parseState{rargs: args.Slice(), posArgs: []string{}}
	for ; st.remaining() > 0; st.rargs = st.rargs[1:] {
		tracef("rearrange:1 (cmd=%[1]q) %[2]q", cmd.Name, st.rargs)

		action, err := cmd.parseNextArg(st)
		switch action {
		case parseStop:
			return &stringSliceArgs{st.posArgs}, err
		case parseBreak:
			return &stringSliceArgs{st.posArgs}, nil
		}
	}

	tracef("returning-2 (cmd=%[1]q) args %[2]q", cmd.Name, st.posArgs)
	return &stringSliceArgs{st.posArgs}, nil
}

// parseNextArg handles the argument at the head of st.rargs, updating st as
// values are consumed and positional args are collected. It returns the action
// the caller should take, and an error to surface when the action is parseStop.
func (cmd *Command) parseNextArg(st *parseState) (parseAction, error) {
	firstArg := strings.TrimSpace(st.first())
	if len(firstArg) == 0 {
		st.push(st.first())
		return parseContinue, nil
	}

	// stop parsing once we see a "--"
	if firstArg == "--" {
		// In shell completion mode, preserve "--" so that completion can detect
		// when the user is completing "--" itself vs. completing after "--"
		if cmd.Root().shellCompletion {
			st.push(firstArg)
		}
		st.pushRestAfterFirst()
		return parseBreak, nil
	}

	// Check if we've reached the Nth argument and should stop flag parsing
	if cmd.StopOnNthArg != nil && len(st.posArgs) == *cmd.StopOnNthArg {
		// Append current arg and all remaining args without parsing
		st.pushRest()
		return parseBreak, nil
	}

	// handle positional args
	if firstArg[0] != '-' {
		return cmd.handlePositionalArg(firstArg, st), nil
	}

	// this is same as firstArg == "-"
	if len(firstArg) == 1 {
		st.push(firstArg)
		return parseBreak, nil
	}

	return cmd.handleFlagArg(firstArg, st)
}

// handlePositionalArg handles a leading argument that is not a flag. When the
// argument names a subcommand, the remaining args are handed to that command.
func (cmd *Command) handlePositionalArg(firstArg string, st *parseState) parseAction {
	// positional argument probably
	tracef("rearrange-3 (cmd=%[1]q) check %[2]q", cmd.Name, firstArg)

	// if there is a command by that name let the command handle the
	// rest of the parsing
	if cmd.Command(firstArg) != nil {
		st.pushRest()
		return parseBreak
	}

	st.push(firstArg)
	return parseContinue
}

// handleFlagArg handles a leading argument that begins with "-" (and is longer
// than a single "-").
func (cmd *Command) handleFlagArg(firstArg string, st *parseState) (parseAction, error) {
	numMinuses := 1
	shortOptionHandling := cmd.useShortOptionHandling()

	// stop parsing -- as short flags
	if firstArg[1] == '-' {
		numMinuses++
		shortOptionHandling = false
	} else if !unicode.IsLetter(rune(firstArg[1])) {
		// this is not a flag
		tracef("parseFlags not a unicode letter. Stop parsing")
		st.pushRest()
		return parseBreak, nil
	}

	tracef("parseFlags (shortOptionHandling=%[1]q)", shortOptionHandling)

	fa := splitFlagArg(firstArg, numMinuses)

	tracef("flagName:2 (fName=%[1]q) (fVal=%[2]q)", fa.name, fa.val)

	if f := cmd.lookupAppliedFlag(fa.name); f != nil {
		return cmd.applyMatchedFlag(f, fa, st)
	}

	// no flag lookup found and short handling is disabled
	if !shortOptionHandling {
		return cmd.handleUnmatchedFlag(fa.name, st)
	}

	return cmd.applyShortFlags(fa, st)
}

// splitFlagArg splits a flag argument into its name and, when present, the
// value provided via "=". numMinuses is the number of leading "-" to strip.
func splitFlagArg(raw string, numMinuses int) flagArg {
	fa := flagArg{raw: raw, name: raw[numMinuses:]}
	tracef("flagName:1 (fName=%[1]q)", fa.name)
	if index := strings.Index(fa.name, "="); index != -1 {
		fa.val = fa.name[index+1:]
		fa.name = fa.name[:index]
		fa.valFromEqual = true
	}
	return fa
}

// applyMatchedFlag applies a flag that was found by exact name lookup.
func (cmd *Command) applyMatchedFlag(f Flag, fa flagArg, st *parseState) (parseAction, error) {
	tracef("Trying flag type (fName=%[1]q) (type=%[2]T)", fa.name, f)

	if fb, ok := f.(boolFlag); ok && fb.IsBoolFlag() {
		flagVal := fa.val
		if flagVal == "" {
			flagVal = "true"
		}
		tracef("parse Apply bool flag (fName=%[1]q) (fVal=%[2]q)", fa.name, flagVal)
		if err := cmd.set(fa.name, f, flagVal); err != nil {
			return parseStop, err
		}
		return parseContinue, nil
	}

	tracef("processing non bool flag (fName=%[1]q)", fa.name)
	flagVal := fa.val
	// not a bool flag so need to get the next arg
	if flagVal == "" && !fa.valFromEqual {
		if st.remaining() == 1 {
			// In shell completion mode, preserve the flag so that DefaultCompleteWithFlags can use it
			// as lastArg and offer suggestions for it.
			if cmd.Root().shellCompletion {
				st.pushRest()
				return parseBreak, nil
			}
			return parseStop, fmt.Errorf("%s%s", argumentNotProvidedErrMsg, fa.raw)
		}
		flagVal = st.takeNextValue()
	}

	tracef("setting non bool flag (fName=%[1]q) (fVal=%[2]q)", fa.name, flagVal)
	if err := cmd.set(fa.name, f, flagVal); err != nil {
		return parseStop, err
	}

	return parseContinue, nil
}

// handleUnmatchedFlag decides what to do with a long flag that was not found
// and cannot be interpreted as combined short flags.
func (cmd *Command) handleUnmatchedFlag(flagName string, st *parseState) (parseAction, error) {
	// In shell completion mode, preserve the partial flag so that DefaultCompleteWithFlags can use it
	// as lastArg and offer suggestions that match the prefix.
	if cmd.Root().shellCompletion {
		st.pushRest()
		return parseBreak, nil
	}
	// When DefaultCommand is set, pass unknown flags through as positional args
	// so the default command can handle them (fixes #2249)
	if cmd.DefaultCommand != "" {
		st.pushRest()
		return parseBreak, nil
	}
	return parseStop, fmt.Errorf("%s%s", providedButNotDefinedErrMsg, flagName)
}

// applyShortFlags splits a run of combined short flags and applies each of
// them in turn.
func (cmd *Command) applyShortFlags(fa flagArg, st *parseState) (parseAction, error) {
	flagName := fa.name
	for index, c := range flagName {
		tracef("processing flag (fName=%[1]q)", string(c))

		sf := cmd.lookupFlag(string(c))
		if sf == nil {
			if index == 0 && cmd.DefaultCommand != "" {
				st.pushRest()
				return parseBreak, nil
			}
			return parseStop, fmt.Errorf("%s%s", providedButNotDefinedErrMsg, flagName)
		}

		isLast := index == len(flagName)-1
		if action, err := cmd.applyShortFlag(sf, string(c), &fa, isLast, st); action == parseStop {
			return parseStop, err
		}
	}

	return parseContinue, nil
}

// applyShortFlag applies a single flag from a run of combined short flags. Only
// the last flag in the run may consume a value from the following argument;
// when it does, fa.val and st are updated accordingly. It returns parseStop
// with an error when the flag cannot be set, and parseContinue otherwise.
func (cmd *Command) applyShortFlag(sf Flag, c string, fa *flagArg, isLast bool, st *parseState) (parseAction, error) {
	if fb, ok := sf.(boolFlag); ok && fb.IsBoolFlag() {
		fv := fa.val
		if fv == "" {
			fv = "true"
		}
		if err := cmd.set(fa.name, sf, fv); err != nil {
			tracef("processing flag.2 (fName=%[1]q)", c)
			return parseStop, err
		}
		return parseContinue, nil
	}

	// only the last flag in the run can take an arg
	if !isLast {
		return parseContinue, nil
	}

	if fa.val == "" {
		if st.remaining() == 1 {
			return parseStop, fmt.Errorf("%s%s", argumentNotProvidedErrMsg, c)
		}
		fa.val = st.takeNextValue()
	}
	tracef("parseFlags (flagName %[1]q) (flagVal %[2]q)", fa.name, fa.val)
	if err := cmd.set(fa.name, sf, fa.val); err != nil {
		tracef("processing flag.4 (fName=%[1]q)", c)
		return parseStop, err
	}
	return parseContinue, nil
}
