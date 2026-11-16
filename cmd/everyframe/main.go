// Command everyframe inspects animated images frame by frame.
//
//	everyframe inspect file.gif          describe the file and its frames
//	everyframe inspect -json file.gif    the same, as JSON
//	everyframe frames -o dir file.gif    write every frame as PNG files
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/vitor-schipani/everyframe/gif"
)

const usage = `usage: everyframe <command> [flags] <file>

commands:
  inspect   describe a GIF: header, frames, metadata and irregularities
  frames    write every frame of a GIF as PNG files

Give "-" as the file to read standard input.

For the flags of a command:
  everyframe <command> -h
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// errUsage marks a command-line mistake that has already been explained on
// standard error.
var errUsage = errors.New("usage error")

// run executes the command line and returns the process exit code: 0 for
// success, 1 when the file could not be fully processed, 2 for a mistake in
// the command line.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch cmd, rest := args[0], args[1:]; cmd {
	case "inspect":
		err = runInspect(rest, stdin, stdout, stderr)
	case "frames":
		err = runFrames(rest, stdin, stdout, stderr)
	case "help", "-h", "-help", "--help":
		if _, err := fmt.Fprint(stdout, usage); err != nil {
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "everyframe: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	}
	_, _ = fmt.Fprintf(stderr, "everyframe: %v\n", err)
	return 1
}

// command is the flag set of one subcommand, which takes a single file.
type command struct {
	*flag.FlagSet
	stderr io.Writer
	limits gif.Limits
}

// newCommand prepares a flag set that reports its errors on stderr and
// carries the flags shared by every subcommand. summary is the one-line
// description shown by -h.
func newCommand(name, summary string, stderr io.Writer) *command {
	c := &command{FlagSet: flag.NewFlagSet(name, flag.ContinueOnError), stderr: stderr}
	c.SetOutput(stderr)
	c.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: everyframe %s [flags] <file>\n\n%s\n\nflags:\n", name, summary)
		c.PrintDefaults()
	}
	def := gif.DefaultLimits()
	c.Int64Var(&c.limits.MaxPixels, "max-pixels", def.MaxPixels, "largest canvas or frame area to accept, in pixels")
	c.IntVar(&c.limits.MaxFrames, "max-frames", def.MaxFrames, "largest number of frames to accept")
	c.Int64Var(&c.limits.MaxDecodedBytes, "max-decoded-bytes", def.MaxDecodedBytes, "most pixel data to decode over the whole file")
	return c
}

// parse reads the flags, which may come before or after the file, and returns
// the file. Mistakes are reported on stderr and returned as errUsage.
func (c *command) parse(args []string) (string, error) {
	var files []string
	for {
		if err := c.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return "", err
			}
			return "", errUsage // the flag package has printed the reason
		}
		if c.NArg() == 0 {
			break
		}
		files = append(files, c.Arg(0))
		args = c.Args()[1:]
	}
	if len(files) != 1 {
		_, _ = fmt.Fprintf(c.stderr, "everyframe %s: expected one file, got %d\n", c.Name(), len(files))
		c.Usage()
		return "", errUsage
	}
	return files[0], nil
}

// open returns the named file, or standard input for "-".
func open(name string, stdin io.Reader) (io.ReadCloser, error) {
	if name == "-" {
		return io.NopCloser(stdin), nil
	}
	return os.Open(name)
}
