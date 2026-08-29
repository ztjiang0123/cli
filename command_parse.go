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

func (cmd *Command) parseFlags(args Args) (Args, error) {
	tracef("parsing flags from arguments %[1]q (cmd=%[2]q)", args, cmd.Name)

	cmd.setFlags = map[Flag]struct{}{}
	cmd.appliedFlags = cmd.allFlags()

	cmd.applyPersistentFlags()

	tracef("parsing flags iteratively tail=%[1]q (cmd=%[2]q)", args.Tail(), cmd.Name)
	defer tracef("done parsing flags (cmd=%[1]q)", cmd.Name)

	posArgs := []string{}
	for rargs := args.Slice(); len(rargs) > 0; rargs = rargs[1:] {
		tracef("rearrange:1 (cmd=%[1]q) %[2]q", cmd.Name, rargs)

		action, err := cmd.parseNextArg(&rargs, &posArgs)
		switch action {
		case parseStop:
			return &stringSliceArgs{posArgs}, err
		case parseBreak:
			return &stringSliceArgs{posArgs}, nil
		}
	}

	tracef("returning-2 (cmd=%[1]q) args %[2]q", cmd.Name, posArgs)
	return &stringSliceArgs{posArgs}, nil
}

// parseNextArg handles the leading argument of *rargs, updating *rargs (when a
// value is consumed from a following argument) and *posArgs. It returns the
// action the caller should take, and an error to surface when the action is
// parseStop.
func (cmd *Command) parseNextArg(rargs *[]string, posArgs *[]string) (parseAction, error) {
	firstArg := strings.TrimSpace((*rargs)[0])
	if len(firstArg) == 0 {
		*posArgs = append(*posArgs, (*rargs)[0])
		return parseContinue, nil
	}

	// stop parsing once we see a "--"
	if firstArg == "--" {
		// In shell completion mode, preserve "--" so that completion can detect
		// when the user is completing "--" itself vs. completing after "--"
		if cmd.Root().shellCompletion {
			*posArgs = append(*posArgs, firstArg)
		}
		*posArgs = append(*posArgs, (*rargs)[1:]...)
		return parseBreak, nil
	}

	// Check if we've reached the Nth argument and should stop flag parsing
	if cmd.StopOnNthArg != nil && len(*posArgs) == *cmd.StopOnNthArg {
		// Append current arg and all remaining args without parsing
		*posArgs = append(*posArgs, (*rargs)[0:]...)
		return parseBreak, nil
	}

	// handle positional args
	if firstArg[0] != '-' {
		return cmd.handlePositionalArg(firstArg, *rargs, posArgs), nil
	}

	// this is same as firstArg == "-"
	if len(firstArg) == 1 {
		*posArgs = append(*posArgs, firstArg)
		return parseBreak, nil
	}

	return cmd.handleFlagArg(firstArg, rargs, posArgs)
}

// handlePositionalArg handles a leading argument that is not a flag. When the
// argument names a subcommand, the remaining args are handed to that command.
func (cmd *Command) handlePositionalArg(firstArg string, rargs []string, posArgs *[]string) parseAction {
	// positional argument probably
	tracef("rearrange-3 (cmd=%[1]q) check %[2]q", cmd.Name, firstArg)

	// if there is a command by that name let the command handle the
	// rest of the parsing
	if cmd.Command(firstArg) != nil {
		*posArgs = append(*posArgs, rargs...)
		return parseBreak
	}

	*posArgs = append(*posArgs, firstArg)
	return parseContinue
}

// handleFlagArg handles a leading argument that begins with "-" (and is longer
// than a single "-").
func (cmd *Command) handleFlagArg(firstArg string, rargs *[]string, posArgs *[]string) (parseAction, error) {
	numMinuses := 1
	shortOptionHandling := cmd.useShortOptionHandling()

	// stop parsing -- as short flags
	if firstArg[1] == '-' {
		numMinuses++
		shortOptionHandling = false
	} else if !unicode.IsLetter(rune(firstArg[1])) {
		// this is not a flag
		tracef("parseFlags not a unicode letter. Stop parsing")
		*posArgs = append(*posArgs, *rargs...)
		return parseBreak, nil
	}

	tracef("parseFlags (shortOptionHandling=%[1]q)", shortOptionHandling)

	flagName, flagVal, valFromEqual := splitFlagArg(firstArg[numMinuses:])

	tracef("flagName:2 (fName=%[1]q) (fVal=%[2]q)", flagName, flagVal)

	if f := cmd.lookupAppliedFlag(flagName); f != nil {
		return cmd.applyMatchedFlag(f, firstArg, flagName, flagVal, valFromEqual, rargs, posArgs)
	}

	// no flag lookup found and short handling is disabled
	if !shortOptionHandling {
		return cmd.handleUnmatchedFlag(flagName, *rargs, posArgs)
	}

	return cmd.applyShortFlags(flagName, flagVal, rargs, posArgs)
}

// splitFlagArg splits a flag argument (with leading minuses already removed)
// into its name and, when present, the value provided via "=".
func splitFlagArg(arg string) (name, val string, valFromEqual bool) {
	name = arg
	tracef("flagName:1 (fName=%[1]q)", name)
	if index := strings.Index(name, "="); index != -1 {
		val = name[index+1:]
		name = name[:index]
		valFromEqual = true
	}
	return name, val, valFromEqual
}

// applyMatchedFlag applies a flag that was found by exact name lookup.
func (cmd *Command) applyMatchedFlag(f Flag, firstArg, flagName, flagVal string, valFromEqual bool, rargs *[]string, posArgs *[]string) (parseAction, error) {
	tracef("Trying flag type (fName=%[1]q) (type=%[2]T)", flagName, f)

	if fb, ok := f.(boolFlag); ok && fb.IsBoolFlag() {
		if flagVal == "" {
			flagVal = "true"
		}
		tracef("parse Apply bool flag (fName=%[1]q) (fVal=%[2]q)", flagName, flagVal)
		if err := cmd.set(flagName, f, flagVal); err != nil {
			return parseStop, err
		}
		return parseContinue, nil
	}

	tracef("processing non bool flag (fName=%[1]q)", flagName)
	// not a bool flag so need to get the next arg
	if flagVal == "" && !valFromEqual {
		if len(*rargs) == 1 {
			// In shell completion mode, preserve the flag so that DefaultCompleteWithFlags can use it
			// as lastArg and offer suggestions for it.
			if cmd.Root().shellCompletion {
				*posArgs = append(*posArgs, *rargs...)
				return parseBreak, nil
			}
			return parseStop, fmt.Errorf("%s%s", argumentNotProvidedErrMsg, firstArg)
		}
		flagVal = (*rargs)[1]
		*rargs = (*rargs)[1:]
	}

	tracef("setting non bool flag (fName=%[1]q) (fVal=%[2]q)", flagName, flagVal)
	if err := cmd.set(flagName, f, flagVal); err != nil {
		return parseStop, err
	}

	return parseContinue, nil
}

// handleUnmatchedFlag decides what to do with a long flag that was not found
// and cannot be interpreted as combined short flags.
func (cmd *Command) handleUnmatchedFlag(flagName string, rargs []string, posArgs *[]string) (parseAction, error) {
	// In shell completion mode, preserve the partial flag so that DefaultCompleteWithFlags can use it
	// as lastArg and offer suggestions that match the prefix.
	if cmd.Root().shellCompletion {
		*posArgs = append(*posArgs, rargs...)
		return parseBreak, nil
	}
	// When DefaultCommand is set, pass unknown flags through as positional args
	// so the default command can handle them (fixes #2249)
	if cmd.DefaultCommand != "" {
		*posArgs = append(*posArgs, rargs...)
		return parseBreak, nil
	}
	return parseStop, fmt.Errorf("%s%s", providedButNotDefinedErrMsg, flagName)
}

// applyShortFlags splits a run of combined short flags and applies each of
// them in turn.
func (cmd *Command) applyShortFlags(flagName, flagVal string, rargs *[]string, posArgs *[]string) (parseAction, error) {
	for index, c := range flagName {
		tracef("processing flag (fName=%[1]q)", string(c))

		sf := cmd.lookupFlag(string(c))
		if sf == nil {
			if index == 0 && cmd.DefaultCommand != "" {
				*posArgs = append(*posArgs, *rargs...)
				return parseBreak, nil
			}
			return parseStop, fmt.Errorf("%s%s", providedButNotDefinedErrMsg, flagName)
		}

		isLast := index == len(flagName)-1
		if action, err := cmd.applyShortFlag(sf, string(c), flagName, &flagVal, isLast, rargs); action == parseStop {
			return parseStop, err
		}
	}

	return parseContinue, nil
}

// applyShortFlag applies a single flag from a run of combined short flags. Only
// the last flag in the run may consume a value from the following argument;
// when it does, *flagVal and *rargs are updated accordingly. It returns
// parseStop with an error when the flag cannot be set, and parseContinue
// otherwise.
func (cmd *Command) applyShortFlag(sf Flag, c, flagName string, flagVal *string, isLast bool, rargs *[]string) (parseAction, error) {
	if fb, ok := sf.(boolFlag); ok && fb.IsBoolFlag() {
		fv := *flagVal
		if fv == "" {
			fv = "true"
		}
		if err := cmd.set(flagName, sf, fv); err != nil {
			tracef("processing flag.2 (fName=%[1]q)", c)
			return parseStop, err
		}
		return parseContinue, nil
	}

	// only the last flag in the run can take an arg
	if !isLast {
		return parseContinue, nil
	}

	if *flagVal == "" {
		if len(*rargs) == 1 {
			return parseStop, fmt.Errorf("%s%s", argumentNotProvidedErrMsg, c)
		}
		*flagVal = (*rargs)[1]
		*rargs = (*rargs)[1:]
	}
	tracef("parseFlags (flagName %[1]q) (flagVal %[2]q)", flagName, *flagVal)
	if err := cmd.set(flagName, sf, *flagVal); err != nil {
		tracef("processing flag.4 (fName=%[1]q)", c)
		return parseStop, err
	}
	return parseContinue, nil
}
