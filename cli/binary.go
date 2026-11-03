package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"time"

	api "github.com/Manu343726/hammer"
)

// DefaultHelpTimeout bounds one help invocation of a binary. A tool that prints
// its help has no reason to be slow, and a tool that hangs would otherwise hang
// the description with it.
const DefaultHelpTimeout = 30 * time.Second

// DescribeBinary reads a description out of a program by asking it, one help page
// at a time.
//
// A tool that is only a binary is still describable, and its own help is the only
// document it publishes. The walk is deliberately narrow: it runs the program
// with --help and with nothing else, so a subcommand is never executed for its
// side effects, and a command that needs an argument to print its help is
// described as far as its parent's page goes rather than run to find out.
//
// The walk is bounded three ways, because a description is not worth an
// unbounded process tree: by MaxDepth, by MaxCommands, and by HelpTimeout per
// invocation on top of the caller's own deadline. A tree that reaches a bound is
// described in part, and Metadata[MetadataTruncated] states which bound was
// reached — a partial description says so rather than passing for a complete one.
func DescribeBinary(ctx context.Context, path string, options DescribeOptions) (api.API, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return api.API{}, api.Errorf(api.KindInvalid, "a program to describe is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	walker := &helpWalker{
		path:        path,
		timeout:     options.HelpTimeout,
		maxDepth:    options.maxDepth(),
		maxCommands: options.maxCommands(),
	}
	if walker.timeout <= 0 {
		walker.timeout = DefaultHelpTimeout
	}
	pages, err := walker.walk(ctx, nil, 0)
	if err != nil {
		return api.API{}, err
	}
	described, err := assemble(pages, options, path)
	if err != nil {
		return api.API{}, err
	}
	if walker.truncated != "" {
		if described.Metadata == nil {
			described.Metadata = map[string]string{}
		}
		described.Metadata[MetadataTruncated] = walker.truncated
	}
	return described, nil
}

// helpWalker is one bounded walk of a program's help.
type helpWalker struct {
	path        string
	timeout     time.Duration
	maxDepth    int
	maxCommands int
	visited     int
	// truncated states the bound that stopped the walk, and is empty for a walk
	// that read everything the tool advertises.
	truncated string
}

func (w *helpWalker) stop(reason string) {
	if w.truncated == "" {
		w.truncated = reason
	}
}

// walk reads one command's page and the pages of the commands it advertises.
func (w *helpWalker) walk(ctx context.Context, prefix []string, depth int) ([]commandPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, api.WrapError(api.KindUnavailable, err, "describe %q", strings.Join(append([]string{w.path}, prefix...), " "))
	}
	if w.visited >= w.maxCommands {
		w.stop("the command bound of " + strconv.Itoa(w.maxCommands) + " was reached")
		return nil, nil
	}
	w.visited++
	pages, err := w.help(ctx, prefix)
	if err != nil {
		return nil, err
	}
	page := pages[0]
	result := []commandPage{page}
	for _, child := range page.children {
		// Cobra's own help command is listed and is not an operation: running it
		// prints another page.
		if child.summary == cobraHelpSummary {
			continue
		}
		if depth+1 > w.maxDepth {
			w.stop("the depth bound of " + strconv.Itoa(w.maxDepth) + " was reached")
			break
		}
		if w.visited >= w.maxCommands {
			w.stop("the command bound of " + strconv.Itoa(w.maxCommands) + " was reached")
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, api.WrapError(api.KindUnavailable, err, "describe %q", w.path)
		}
		invocation := append(append([]string(nil), prefix...), child.name)
		sub, err := w.walk(ctx, invocation, depth+1)
		if err != nil {
			return nil, err
		}
		result = append(result, sub...)
	}
	return result, nil
}

// help reads one page.
//
// The only argument ever passed to the program is --help, and it is passed last:
// a description is taken from what the tool says about itself, never from what
// running it would do.
func (w *helpWalker) help(ctx context.Context, prefix []string) ([]commandPage, error) {
	runCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	invocation := append(append([]string(nil), prefix...), "--help")
	command := exec.CommandContext(runCtx, w.path, invocation...)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	command.Stdout, command.Stderr = stdout, stderr
	// A page is copied through a pipe, and a grandchild that inherits it would
	// otherwise keep the read open after the command itself has gone.
	command.WaitDelay = time.Second
	err := command.Run()

	name := strings.Join(append([]string{w.path}, invocation...), " ")
	text := stdout.String()
	if strings.TrimSpace(text) == "" {
		// A tool that writes its help to the diagnostic stream has still stated
		// it, and refusing that would refuse a whole class of tools.
		text = stderr.String()
	}
	switch {
	case runCtx.Err() != nil:
		return nil, api.WrapError(api.KindUnavailable, runCtx.Err(),
			"%s did not answer within %s", name, w.timeout)
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return nil, api.WrapError(api.KindNotFound, err, "%s was not found", w.path)
	}
	pages := parseHelpPages(text)
	if len(pages) == 0 {
		if err != nil {
			return nil, api.WrapError(api.KindFailedPrecondition, err,
				"%s printed no help page this reader recognizes", name)
		}
		return nil, api.Errorf(api.KindFailedPrecondition,
			"%s printed no help page this reader recognizes", name)
	}
	return pages, nil
}
