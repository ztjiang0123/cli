package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"unicode"
)

type helpShownKey struct{}

// stdinArgParser tokenizes the flags/arguments read from a command's stdin. It
// is a small state machine that reads runes one at a time, splitting on
// whitespace while honoring double-quoted strings.
type stdinArgParser struct {
	inString bool     // currently reading the body of a quoted string
	token    string   // the token accumulated so far
	args     []string // completed tokens
}

// flushToken appends the accumulated token to args (when non-empty) and resets
// it. It reports whether a "--" terminator was flushed, which signals the
// caller to stop parsing.
func (p *stdinArgParser) flushToken() (terminated bool) {
	if p.token == "" {
		return false
	}
	if !p.inString && p.token == "--" {
		return true
	}
	p.args = append(p.args, p.token)
	p.token = ""
	return false
}

// consume processes a single rune. It reports whether parsing should stop (a
// "--" terminator was reached).
func (p *stdinArgParser) consume(ch rune) (terminated bool) {
	if p.inString {
		if ch == '"' {
			p.flushToken()
			p.inString = false
		} else {
			p.token += string(ch)
		}
		return false
	}

	// Outside a quoted string, whitespace and quotes delimit tokens.
	if unicode.IsSpace(ch) || ch == '"' {
		if p.flushToken() {
			return true
		}
		if ch == '"' {
			p.inString = true
		}
		return false
	}

	p.token += string(ch)
	return false
}

// finish handles the trailing token once EOF is reached.
func (p *stdinArgParser) finish() {
	if p.inString {
		// An unterminated quoted string is kept only if it has content.
		for _, t := range p.token {
			if !unicode.IsSpace(t) {
				p.args = append(p.args, p.token)
				break
			}
		}
		return
	}

	if p.token != "--" {
		p.args = append(p.args, p.token)
	}
}

func (cmd *Command) parseArgsFromStdin() ([]string, error) {
	p := &stdinArgParser{args: []string{}}
	breader := bufio.NewReader(cmd.Reader)

	for {
		ch, _, err := breader.ReadRune()
		if err == io.EOF {
			p.finish()
			break
		}
		if err != nil {
			return nil, err
		}
		if p.consume(ch) {
			break
		}
	}

	tracef("parsed stdin args as %v (cmd=%[2]q)", p.args, cmd.Name)

	return p.args, nil
}

// Run is the entry point to the command graph. The positional
// arguments are parsed according to the Flag and Command
// definitions and the matching Action functions are run.
func (cmd *Command) Run(ctx context.Context, osArgs []string) (deferErr error) {
	_, deferErr = cmd.run(ctx, osArgs)
	return deferErr
}

func (cmd *Command) run(ctx context.Context, osArgs []string) (_ context.Context, deferErr error) {
	tracef("running with arguments %[1]q (cmd=%[2]q)", osArgs, cmd.Name)
	cmd.setupDefaults(osArgs)

	// Validate StopOnNthArg
	if cmd.StopOnNthArg != nil && *cmd.StopOnNthArg < 0 {
		return ctx, fmt.Errorf("StopOnNthArg must be non-negative, got %d", *cmd.StopOnNthArg)
	}

	if v, ok := ctx.Value(commandContextKey).(*Command); ok {
		tracef("setting parent (cmd=%[1]q) command from context.Context value (cmd=%[2]q)", v.Name, cmd.Name)
		cmd.parent = v
	}

	if cmd.parent == nil {
		var err error
		if osArgs, err = cmd.setupRootArgs(osArgs); err != nil {
			return ctx, err
		}
	}

	tracef("using post-checkShellCompleteFlag arguments %[1]q (cmd=%[2]q)", osArgs, cmd.Name)

	tracef("setting self as cmd in context (cmd=%[1]q)", cmd.Name)
	ctx = context.WithValue(ctx, commandContextKey, cmd)

	if cmd.parent == nil {
		cmd.setupCommandGraph()
	}

	var rargs Args = &stringSliceArgs{v: osArgs}
	var args Args = &stringSliceArgs{rargs.Tail()}

	if err := cmd.preParseFlags(); err != nil {
		return ctx, err
	}

	var err error

	if cmd.SkipFlagParsing {
		tracef("skipping flag parsing (cmd=%[1]q)", cmd.Name)
		cmd.parsedArgs = args
	} else {
		cmd.parsedArgs, err = cmd.parseFlags(args)
	}

	tracef("using post-parse arguments %[1]q (cmd=%[2]q)", args, cmd.Name)

	if shouldRunCompletion(cmd) {
		return cmd.runCompletionPhase(ctx)
	}

	if err != nil {
		return cmd.handleParseError(ctx, err)
	}

	if cmd.checkHelp() {
		ctx = context.WithValue(ctx, helpShownKey{}, true)
		return ctx, helpCommandAction(ctx, cmd)
	}
	tracef("no help is wanted (cmd=%[1]q)", cmd.Name)

	if cmd.parent == nil && !cmd.HideVersion && checkVersion(cmd) {
		ShowVersion(cmd)
		return ctx, nil
	}

	if err := cmd.postParseFlags(); err != nil {
		return ctx, err
	}

	if cmd.After != nil && !cmd.Root().shellCompletion {
		defer func() {
			deferErr = cmd.runAfter(ctx, deferErr)
		}()
	}

	if newCtx, err := cmd.checkMutuallyExclusiveFlags(ctx); err != nil {
		return newCtx, err
	}

	subCmd := cmd.resolveSubCommand()

	// If a subcommand has been resolved, let it handle the remaining execution.
	if subCmd != nil {
		tracef("running sub-command %[1]q with arguments %[2]q (cmd=%[3]q)", subCmd.Name, cmd.Args(), cmd.Name)

		// It is important that we overwrite the ctx variable in the current
		// function so any defer'd functions use the new context returned
		// from the sub command.
		ctx, err = subCmd.run(ctx, cmd.Args().Slice())
		return ctx, err
	}

	// This code path is the innermost command execution: run the leaf
	// command's own action. executeCommand may advance ctx (via Before
	// actions); assign it back so the deferred After call sees the same
	// context.
	ctx, deferErr = cmd.executeCommand(ctx)

	tracef("returning deferErr (cmd=%[1]q) %[2]q", cmd.Name, deferErr)
	return ctx, deferErr
}

// executeCommand runs the leaf command: ancestor ArgValidator, Before actions,
// flag actions, required-flag/argument checks, positional argument parsing, and
// finally the command's Action. It returns the (possibly advanced) context and
// the error to surface as deferErr.
func (cmd *Command) executeCommand(ctx context.Context) (context.Context, error) {
	// Resolve the chain of nested commands up to the parent.
	cmdChain := commandChain(cmd)

	// Run ArgValidator from the nearest ancestor that sets one.
	if validator := findArgValidator(cmd); validator != nil {
		if err := validator(ctx, cmd); err != nil {
			return ctx, cmd.handleExitCoder(ctx, err)
		}
	}

	// Run Before actions in order.
	var err error
	if ctx, err = runBefore(ctx, cmdChain); err != nil {
		return ctx, err
	}

	// Run flag actions in order.
	// These take a context, so this has to happen after Before actions.
	for _, c := range cmdChain {
		tracef("running flag actions (cmd=%[1]q)", c.Name)
		if err := c.runFlagActions(ctx); err != nil {
			return ctx, c.handleExitCoder(ctx, err)
		}
	}

	if requiredErr := cmd.checkRequirements(); requiredErr != nil {
		return cmd.handleRequiredError(ctx, requiredErr)
	}

	// Parse positional arguments, if the command declares any.
	if len(cmd.Arguments) > 0 {
		if newCtx, err := cmd.parseArguments(ctx); err != nil {
			return newCtx, err
		}
	}

	if err := cmd.Action(ctx, cmd); err != nil {
		tracef("calling handleExitCoder with %[1]v (cmd=%[2]q)", err, cmd.Name)
		return ctx, cmd.handleExitCoder(ctx, err)
	}

	return ctx, nil
}

// setupRootArgs performs the argument preparation that only applies to the root
// command: optionally reading extra args from stdin, then splitting off the
// shell-completion flag before regular flag parsing sees it.
func (cmd *Command) setupRootArgs(osArgs []string) ([]string, error) {
	if cmd.ReadArgsFromStdin {
		args, err := cmd.parseArgsFromStdin()
		if err != nil {
			return nil, err
		}
		osArgs = append(osArgs, args...)
	}

	// handle the completion flag separately from the flagset since
	// completion could be attempted after a flag, but before its value was put
	// on the command line. this causes the flagset to interpret the completion
	// flag name as the value of the flag before it which is undesirable
	// note that we can only do this because the shell autocomplete function
	// always appends the completion flag at the end of the command
	tracef("checking osArgs %v (cmd=%[2]q)", osArgs, cmd.Name)
	cmd.shellCompletion, osArgs = checkShellCompleteFlag(cmd, osArgs)

	tracef("setting cmd.shellCompletion=%[1]v from checkShellCompleteFlag (cmd=%[2]q)", cmd.shellCompletion && cmd.EnableShellCompletion, cmd.Name)
	cmd.shellCompletion = cmd.EnableShellCompletion && cmd.shellCompletion

	return osArgs, nil
}

// preParseFlags runs PreParse on every flag owned by this command, skipping
// flags that are persistent flags inherited from an ancestor.
func (cmd *Command) preParseFlags() error {
	for _, f := range cmd.allFlags() {
		if cmd.hasPersistentFlagOnAncestor(f) {
			continue
		}
		if err := f.PreParse(); err != nil {
			return err
		}
	}
	return nil
}

// runCompletionPhase runs the Before actions and then shell completion,
// returning the context and error the caller should propagate.
func (cmd *Command) runCompletionPhase(ctx context.Context) (context.Context, error) {
	var beforeErr error
	if ctx, beforeErr = runBefore(ctx, commandChain(cmd)); beforeErr != nil {
		return ctx, beforeErr
	}
	runCompletion(ctx, cmd)
	return ctx, nil
}

// postParseFlags runs PostParse on every flag, applies multi-value parsing
// config, and records flags that became set via the environment.
func (cmd *Command) postParseFlags() error {
	for _, flag := range cmd.allFlags() {
		cmd.setMultiValueParsingConfig(flag)
		isSet := flag.IsSet()
		if err := flag.PostParse(); err != nil {
			return err
		}
		// add env set flags here
		if !isSet && flag.IsSet() {
			cmd.setFlags[flag] = struct{}{}
		}
	}
	return nil
}

// runAfter executes the command's After action (invoked from a deferred call)
// and folds any resulting error into the pending deferErr. Help output short
// circuits the After action.
func (cmd *Command) runAfter(ctx context.Context, deferErr error) error {
	if ctx.Value(helpShownKey{}) != nil {
		return deferErr
	}
	err := cmd.After(ctx, cmd)
	if err == nil {
		return deferErr
	}
	err = cmd.handleExitCoder(ctx, err)
	if deferErr != nil {
		return newMultiError(deferErr, err)
	}
	return err
}

// checkRequirements verifies that all required flags and arguments are present,
// returning the first violation encountered (or nil).
func (cmd *Command) checkRequirements() error {
	if err := cmd.checkAllRequiredFlags(); err != nil {
		return err
	}
	return cmd.checkRequiredArguments()
}

// handleParseError renders the appropriate help/usage output for a flag parsing
// error and returns the context and error the caller should propagate.
func (cmd *Command) handleParseError(ctx context.Context, err error) (context.Context, error) {
	tracef("setting deferErr from %[1]q (cmd=%[2]q)", err, cmd.Name)
	cmd.isInError = true

	if cmd.checkHelp() {
		ctx = context.WithValue(ctx, helpShownKey{}, true)
		if cmd.parent == nil {
			_ = ShowRootCommandHelp(cmd)
		} else {
			_ = ShowSubcommandHelp(cmd)
		}
		return ctx, nil
	}

	if cmd.OnUsageError != nil {
		err = cmd.OnUsageError(ctx, cmd, err, cmd.parent != nil)
		return ctx, cmd.handleExitCoder(ctx, err)
	}

	fmt.Fprintf(cmd.Root().ErrWriter, "Incorrect Usage: %s\n\n", err.Error())
	if cmd.Suggest {
		if suggestion, sErr := cmd.suggestFlagFromError(err, ""); sErr == nil {
			fmt.Fprintf(cmd.Root().ErrWriter, "%s", suggestion)
		}
	}
	if !cmd.hideHelp() {
		cmd.showHelpForError()
	}

	return ctx, err
}

// showHelpForError prints the root or subcommand help after a usage error.
func (cmd *Command) showHelpForError() {
	if cmd.parent == nil {
		tracef("running ShowRootCommandHelp")
		if err := ShowRootCommandHelp(cmd); err != nil {
			tracef("SILENTLY IGNORING ERROR running ShowRootCommandHelp %[1]v (cmd=%[2]q)", err, cmd.Name)
		}
		return
	}
	tracef("running ShowSubcommandHelp for %[1]q", cmd.Name)
	_ = ShowSubcommandHelp(cmd)
}

// checkMutuallyExclusiveFlags walks the parent chain and validates every
// mutually exclusive flag group, since persistent flags are inherited by
// descendants. On violation it renders usage output and returns the error.
func (cmd *Command) checkMutuallyExclusiveFlags(ctx context.Context) (context.Context, error) {
	for pCmd := cmd; pCmd != nil; pCmd = pCmd.parent {
		for _, grp := range pCmd.MutuallyExclusiveFlags {
			err := grp.check(cmd)
			if err == nil {
				continue
			}
			if cmd.OnUsageError != nil {
				return ctx, cmd.OnUsageError(ctx, cmd, err, cmd.parent != nil)
			}
			fmt.Fprintf(cmd.Root().ErrWriter, "Incorrect Usage: %s\n\n", err.Error())
			if cmd.parent == nil {
				_ = ShowRootCommandHelp(cmd)
			} else if helpErr := ShowCommandHelp(ctx, cmd.parent, cmd.Name); helpErr != nil {
				_ = ShowSubcommandHelp(cmd)
			}
			return ctx, err
		}
	}
	return ctx, nil
}

// resolveSubCommand selects the sub-command to dispatch to based on the parsed
// positional arguments and any configured default command. It returns nil when
// this command should execute its own action.
func (cmd *Command) resolveSubCommand() *Command {
	if cmd.parsedArgs.Present() {
		return cmd.subCommandForArgs()
	}

	if cmd.DefaultCommand != "" {
		tracef("no positional args present; checking default command %[1]q (cmd=%[2]q)", cmd.DefaultCommand, cmd.Name)
		if dc := cmd.Command(cmd.DefaultCommand); dc != cmd {
			return dc
		}
	}

	return nil
}

// subCommandForArgs resolves a sub-command from the first positional argument,
// falling back to the default command when no direct match is found.
func (cmd *Command) subCommandForArgs() *Command {
	tracef("checking positional args %[1]q (cmd=%[2]q)", cmd.parsedArgs, cmd.Name)

	name := cmd.parsedArgs.First()
	tracef("using first positional argument as sub-command name=%[1]q (cmd=%[2]q)", name, cmd.Name)

	if cmd.SuggestCommandFunc != nil && name != "--" {
		name = cmd.SuggestCommandFunc(cmd.Commands, name)
		tracef("suggested command name=%1[q] (cmd=%[2]q)", name, cmd.Name)
	}

	if subCmd := cmd.Command(name); subCmd != nil {
		return subCmd
	}

	if cmd.DefaultCommand == "" {
		return nil
	}

	tracef("using default command=%[1]q (cmd=%[2]q)", cmd.DefaultCommand, cmd.Name)
	argsWithDefault := cmd.argsWithDefaultCommand(cmd.parsedArgs)
	tracef("using default command args=%[1]q (cmd=%[2]q)", argsWithDefault, cmd.Name)
	cmd.parsedArgs = argsWithDefault
	return cmd.Command(argsWithDefault.First())
}

// parseArguments runs each declared Argument parser over the remaining
// positional args, updating cmd.parsedArgs on success. On failure it renders
// the appropriate usage output and returns the error.
func (cmd *Command) parseArguments(ctx context.Context) (context.Context, error) {
	rargs := cmd.Args().Slice()
	tracef("calling argparse with %[1]v", rargs)

	for _, arg := range cmd.Arguments {
		var err error
		rargs, err = arg.Parse(rargs)
		if err == nil {
			continue
		}

		tracef("calling with %[1]v (cmd=%[2]q)", err, cmd.Name)
		if _, ok := err.(*errRequiredArguments); ok {
			return cmd.handleRequiredError(ctx, err)
		}
		if cmd.OnUsageError != nil {
			err = cmd.OnUsageError(ctx, cmd, err, cmd.parent != nil)
		}
		return ctx, cmd.handleExitCoder(ctx, err)
	}

	cmd.parsedArgs = &stringSliceArgs{v: rargs}
	return ctx, nil
}

func (cmd *Command) handleRequiredError(ctx context.Context, err error) (context.Context, error) {
	cmd.isInError = true
	if cmd.OnUsageError != nil {
		err = cmd.OnUsageError(ctx, cmd, err, cmd.parent != nil)
	} else {
		fmt.Fprintf(cmd.Root().ErrWriter, "Incorrect Usage: %s\n\n", err.Error())
		if cmd.parent == nil {
			_ = ShowRootCommandHelp(cmd)
		} else if helpErr := ShowCommandHelp(ctx, cmd.parent, cmd.Name); helpErr != nil {
			_ = ShowSubcommandHelp(cmd)
		}
	}
	return ctx, err
}

func commandChain(cmd *Command) []*Command {
	var cmdChain []*Command
	for p := cmd; p != nil; p = p.parent {
		cmdChain = append(cmdChain, p)
	}
	slices.Reverse(cmdChain)
	return cmdChain
}

func findArgValidator(cmd *Command) ArgValidatorFunc {
	for c := cmd; c != nil; c = c.parent {
		if c.ArgValidator != nil {
			return c.ArgValidator
		}
	}
	return nil
}

func runBefore(ctx context.Context, cmdChain []*Command) (context.Context, error) {
	for _, cmd := range cmdChain {
		if cmd.Before == nil {
			continue
		}
		if bctx, err := cmd.Before(ctx, cmd); err != nil {
			return ctx, cmd.handleExitCoder(ctx, err)
		} else if bctx != nil {
			ctx = bctx
		}
	}
	return ctx, nil
}
