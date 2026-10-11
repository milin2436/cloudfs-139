package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/milin2436/cloudfs-139/sdk/cloudfs"
)

// PrintWriter writes to stdout, stderr or a file and remembers the first error.
//
// It exists so that an operation's destination is just an io.Writer and an
// error that only shows up on Close is still reported.
type PrintWriter struct {
	w     io.Writer
	close func() error
	err   error
}

// NewWriter builds a writer. An empty dest or "-" means stdout.
func NewWriter(dest string) (*PrintWriter, error) {
	if dest == "" || dest == "-" {
		return &PrintWriter{w: io.Writer(os.Stdout)}, nil
	}
	f, err := os.Create(dest)
	if err != nil {
		return nil, err
	}
	return &PrintWriter{w: io.Writer(f), close: f.Close}, nil
}

// NewErrWriter writes to stderr.
func NewErrWriter() *PrintWriter {
	return &PrintWriter{w: io.Writer(os.Stderr)}
}

func (p *PrintWriter) Write(b []byte) (int, error) {
	if p.err != nil {
		return len(b), p.err
	}
	n, err := p.w.Write(b)
	if err != nil {
		p.err = err
	}
	return n, err
}

// Close flushes the destination and returns the first error seen.
func (p *PrintWriter) Close() error {
	if p.err != nil {
		return p.err
	}
	if p.close != nil {
		p.err = p.close()
	}
	return p.err
}

func (p *PrintWriter) Error() error {
	return p.err
}

// newClient loads the config file and boots the framework.
func newClient(ctx context.Context) (*cloudfs.Client, error) {
	return cloudfs.NewClientFromFile(ctx, configPath)
}

// humanSize renders a byte count for a terminal.
func humanSize(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// atMost returns an Args validator.
//
// cobra v1.10.2 has NoArgs / ExactArgs / ArbitraryArgs / OnlyValidArgs but no
// MaximumArgs, so "zero or one" needs a hand-written validator.
func atMost(n int) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return fmt.Errorf("%s accepts at most %d arg, received %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

// firstArg returns args[0] when present, else def.
func firstArg(args []string, def string) string {
	if len(args) > 0 {
		return args[0]
	}
	return def
}

// confirm asks a yes/no question. EOF on stdin counts as no, so a piped run
// never deletes anything unexpectedly.
func confirm(prompt string) bool {
	fmt.Fprint(os.Stdout, prompt)
	var ans string
	_, _ = fmt.Fscanln(os.Stdin, &ans)
	ans = strings.TrimSpace(strings.ToLower(ans))
	return ans == "y" || ans == "yes"
}
