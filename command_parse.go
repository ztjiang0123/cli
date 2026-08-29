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

// flagParseAction tells parseFlags's argument loop how to proceed after a
// single token has been processed.
type flagParseAction int

const (
	// flagParseNext moves on to the next argument.
	flagParseNext flagParseAction = iota
	// flagParseStop stops the loop and falls through to the normal return.
	flagParseStop
	// flagParseDone returns the accumulated args (and err) immediately.
	flagParseDone
)

// flagParseState holds the mutable state threaded through the argument loop of
// parseFlags. rargs is the not-yet-consumed slice of raw arguments and posArgs
// collects positional (non-flag) arguments in order.
type flagParseState struct {
	rargs   []string
	posArgs []string
}

// result builds the Args value returned to callers from the collected
// positional arguments.
func (s *flagParseState) result() Args {
	return &stringSliceArgs{s.posArgs}
}

// parsedFlag describes a single flag token after its leading dashes have been
// stripped and any inline "=value" has been split out. raw is the original
// token (including dashes) used for error messages.
type parsedFlag struct {
	raw          string
	name         string
	value        string
	valFromEqual bool
}

func (cmd *Command) parseFlags(args Args) (Args, error) {
	tracef("parsing flags from arguments %[1]q (cmd=%[2]q)", args, cmd.Name)

	cmd.setFlags = map[Flag]struct{}{}
	cmd.appliedFlags = cmd.allFlags()

	cmd.applyPersistentFlags()

	tracef("parsing flags iteratively tail=%[1]q (cmd=%[2]q)", args.Tail(), cmd.Name)
	defer tracef("done parsing flags (cmd=%[1]q)", cmd.Name)

	state := &flagParseState{rargs: args.Slice(), posArgs: []string{}}
	for len(state.rargs) > 0 {
		action, err := cmd.parseFlagToken(state)
		if err != nil {
			return state.result(), err
		}
		if action == flagParseDone {
			return state.result(), nil
		}
		if action == flagParseStop {
			break
		}
		state.rargs = state.rargs[1:]
	}

	tracef("returning-2 (cmd=%[1]q) args %[2]q", cmd.Name, state.posArgs)
	return state.result(), nil
}

// applyPersistentFlags walks the command lineage and appends any ancestor
// persistent flags that are not shadowed by a local flag on cmd to the set of
// applied flags.
func (cmd *Command) applyPersistentFlags() {
	tracef("walking command lineage for persistent flags (cmd=%[1]q)", cmd.Name)

	for pCmd := cmd.parent; pCmd != nil; pCmd = pCmd.parent {
		tracef(
			"checking ancestor command=%[1]q for persistent flags (cmd=%[2]q)",
			pCmd.Name, cmd.Name,
		)

		for _, fl := range pCmd.allFlags() {
			cmd.applyPersistentFlag(pCmd, fl)
		}
	}
}

// applyPersistentFlag appends fl (owned by ancestor pCmd) to cmd.appliedFlags
// when fl is persistent and is not shadowed by a local flag of the same name.
func (cmd *Command) applyPersistentFlag(pCmd *Command, fl Flag) {
	flNames := fl.Names()

	pfl, ok := fl.(LocalFlag)
	if !ok || pfl.IsLocal() {
		tracef("skipping non-persistent flag %[1]q (cmd=%[2]q)", flNames, cmd.Name)
		return
	}

	tracef(
		"checking for applying persistent flag=%[1]q pCmd=%[2]q (cmd=%[3]q)",
		flNames, pCmd.Name, cmd.Name,
	)

	for _, name := range flNames {
		if cmd.lFlag(name) != nil {
			tracef("not applying as persistent flag=%[1]q (cmd=%[2]q)", flNames, cmd.Name)
			return
		}
	}

	tracef("applying as persistent flag=%[1]q (cmd=%[2]q)", flNames, cmd.Name)

	tracef("appending to applied flags flag=%[1]q (cmd=%[2]q)", flNames, cmd.Name)
	cmd.appliedFlags = append(cmd.appliedFlags, fl)
}

// parseFlagToken processes the argument at the head of state.rargs, updating
// state and reporting how the loop should continue.
func (cmd *Command) parseFlagToken(state *flagParseState) (flagParseAction, error) {
	tracef("rearrange:1 (cmd=%[1]q) %[2]q", cmd.Name, state.rargs)

	rargs := state.rargs
	firstArg := strings.TrimSpace(rargs[0])
	if len(firstArg) == 0 {
		state.posArgs = append(state.posArgs, rargs[0])
		return flagParseNext, nil
	}

	// stop parsing once we see a "--"
	if firstArg == "--" {
		// In shell completion mode, preserve "--" so that completion can detect
		// when the user is completing "--" itself vs. completing after "--"
		if cmd.Root().shellCompletion {
			state.posArgs = append(state.posArgs, firstArg)
		}
		state.posArgs = append(state.posArgs, rargs[1:]...)
		return flagParseDone, nil
	}

	// Check if we've reached the Nth argument and should stop flag parsing
	if cmd.StopOnNthArg != nil && len(state.posArgs) == *cmd.StopOnNthArg {
		// Append current arg and all remaining args without parsing
		state.posArgs = append(state.posArgs, rargs[0:]...)
		return flagParseDone, nil
	}

	// handle positional args
	if firstArg[0] != '-' {
		return cmd.parsePositionalArg(state, firstArg), nil
	}

	numMinuses := 1
	// this is same as firstArg == "-"
	if len(firstArg) == 1 {
		state.posArgs = append(state.posArgs, firstArg)
		return flagParseStop, nil
	}

	shortOptionHandling := cmd.useShortOptionHandling()

	// stop parsing -- as short flags
	if firstArg[1] == '-' {
		numMinuses++
		shortOptionHandling = false
	} else if !unicode.IsLetter(rune(firstArg[1])) {
		// this is not a flag
		tracef("parseFlags not a unicode letter. Stop parsing")
		state.posArgs = append(state.posArgs, rargs...)
		return flagParseDone, nil
	}

	tracef("parseFlags (shortOptionHandling=%[1]q)", shortOptionHandling)

	pf := splitFlagNameValue(firstArg, firstArg[numMinuses:])

	tracef("flagName:2 (fName=%[1]q) (fVal=%[2]q)", pf.name, pf.value)

	if f := cmd.lookupAppliedFlag(pf.name); f != nil {
		return cmd.applyAppliedFlag(state, f, pf)
	}

	// no flag lookup found and short handling is disabled
	if !shortOptionHandling {
		return cmd.handleUnknownLongFlag(state, pf.name)
	}

	return cmd.splitShortFlags(state, pf.name, pf.value)
}

// splitFlagNameValue splits a flag token into a parsedFlag. raw is the original
// token (with leading dashes); token is the same value with leading dashes
// removed, from which the name and any inline "=value" are taken.
func splitFlagNameValue(raw, token string) parsedFlag {
	pf := parsedFlag{raw: raw, name: token}
	tracef("flagName:1 (fName=%[1]q)", pf.name)
	if index := strings.Index(pf.name, "="); index != -1 {
		pf.value = pf.name[index+1:]
		pf.name = pf.name[:index]
		pf.valFromEqual = true
	}
	return pf
}

// parsePositionalArg records a positional (non-flag) argument, or hands the
// remaining args to a matching subcommand.
func (cmd *Command) parsePositionalArg(state *flagParseState, firstArg string) flagParseAction {
	// positional argument probably
	tracef("rearrange-3 (cmd=%[1]q) check %[2]q", cmd.Name, firstArg)

	// if there is a command by that name let the command handle the
	// rest of the parsing
	if cmd.Command(firstArg) != nil {
		state.posArgs = append(state.posArgs, state.rargs...)
		return flagParseDone
	}

	state.posArgs = append(state.posArgs, firstArg)
	return flagParseNext
}

// applyAppliedFlag sets an already-known flag f, consuming a following argument
// as its value when required.
func (cmd *Command) applyAppliedFlag(state *flagParseState, f Flag, pf parsedFlag) (flagParseAction, error) {
	flagName, flagVal := pf.name, pf.value

	tracef("Trying flag type (fName=%[1]q) (type=%[2]T)", flagName, f)
	if fb, ok := f.(boolFlag); ok && fb.IsBoolFlag() {
		if flagVal == "" {
			flagVal = "true"
		}
		tracef("parse Apply bool flag (fName=%[1]q) (fVal=%[2]q)", flagName, flagVal)
		if err := cmd.set(flagName, f, flagVal); err != nil {
			return flagParseNext, err
		}
		return flagParseNext, nil
	}

	tracef("processing non bool flag (fName=%[1]q)", flagName)
	// not a bool flag so need to get the next arg
	if flagVal == "" && !pf.valFromEqual {
		if len(state.rargs) == 1 {
			// In shell completion mode, preserve the flag so that DefaultCompleteWithFlags can use it
			// as lastArg and offer suggestions for it.
			if cmd.Root().shellCompletion {
				state.posArgs = append(state.posArgs, state.rargs...)
				return flagParseDone, nil
			}
			return flagParseNext, fmt.Errorf("%s%s", argumentNotProvidedErrMsg, pf.raw)
		}
		flagVal = state.rargs[1]
		state.rargs = state.rargs[1:]
	}

	tracef("setting non bool flag (fName=%[1]q) (fVal=%[2]q)", flagName, flagVal)
	if err := cmd.set(flagName, f, flagVal); err != nil {
		return flagParseNext, err
	}

	return flagParseNext, nil
}

// handleUnknownLongFlag deals with a "--name" token that did not match any
// applied flag while short-option handling is disabled.
func (cmd *Command) handleUnknownLongFlag(state *flagParseState, flagName string) (flagParseAction, error) {
	// In shell completion mode, preserve the partial flag so that DefaultCompleteWithFlags can use it
	// as lastArg and offer suggestions that match the prefix.
	if cmd.Root().shellCompletion {
		state.posArgs = append(state.posArgs, state.rargs...)
		return flagParseDone, nil
	}
	// When DefaultCommand is set, pass unknown flags through as positional args
	// so the default command can handle them (fixes #2249)
	if cmd.DefaultCommand != "" {
		state.posArgs = append(state.posArgs, state.rargs...)
		return flagParseDone, nil
	}
	return flagParseNext, fmt.Errorf("%s%s", providedButNotDefinedErrMsg, flagName)
}

// splitShortFlags parses a run of combined short flags such as "-abc",
// applying each and letting the final one consume a value when needed.
func (cmd *Command) splitShortFlags(state *flagParseState, flagName, flagVal string) (flagParseAction, error) {
	for index, c := range flagName {
		action, err := cmd.applyShortFlag(state, flagName, flagVal, index, c)
		if err != nil {
			return flagParseNext, err
		}
		if action == flagParseDone {
			return flagParseDone, nil
		}
	}
	return flagParseNext, nil
}

// applyShortFlag applies a single character c from a combined short-flag run.
func (cmd *Command) applyShortFlag(state *flagParseState, flagName, flagVal string, index int, c rune) (flagParseAction, error) {
	tracef("processing flag (fName=%[1]q)", string(c))

	sf := cmd.lookupFlag(string(c))
	if sf == nil {
		if index == 0 && cmd.DefaultCommand != "" {
			state.posArgs = append(state.posArgs, state.rargs...)
			return flagParseDone, nil
		}
		return flagParseNext, fmt.Errorf("%s%s", providedButNotDefinedErrMsg, flagName)
	}

	if fb, ok := sf.(boolFlag); ok && fb.IsBoolFlag() {
		fv := flagVal
		if fv == "" {
			fv = "true"
		}
		if err := cmd.set(flagName, sf, fv); err != nil {
			tracef("processing flag.2 (fName=%[1]q)", string(c))
			return flagParseNext, err
		}
		return flagParseNext, nil
	}

	// last flag can take an arg
	if index != len(flagName)-1 {
		return flagParseNext, nil
	}

	if flagVal == "" {
		if len(state.rargs) == 1 {
			return flagParseNext, fmt.Errorf("%s%s", argumentNotProvidedErrMsg, string(c))
		}
		flagVal = state.rargs[1]
		state.rargs = state.rargs[1:]
	}
	tracef("parseFlags (flagName %[1]q) (flagVal %[2]q)", flagName, flagVal)
	if err := cmd.set(flagName, sf, flagVal); err != nil {
		tracef("processing flag.4 (fName=%[1]q)", string(c))
		return flagParseNext, err
	}
	return flagParseNext, nil
}
