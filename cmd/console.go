package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/console"
)

var consoleCmd = func() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "console VM",
		Short: "Attach interactive console to a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  runConsole,
	}
	cmd.Flags().String("escape-char", "^]", "escape character (single char or ^X caret notation)")
	return cmd
}()

func runConsole(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)
	hyper, err := initHypervisor()
	if err != nil {
		return err
	}
	ref := args[0]

	ptyPath, err := hyper.Console(ctx, ref)
	if err != nil {
		return fmt.Errorf("console: %w", err)
	}

	pty, err := os.OpenFile(ptyPath, os.O_RDWR, 0) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open PTY %s: %w", ptyPath, err)
	}
	defer pty.Close() //nolint:errcheck

	escapeStr, _ := cmd.Flags().GetString("escape-char")
	escapeChar, err := console.ParseEscapeChar(escapeStr)
	if err != nil {
		return err
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Fprintf(os.Stderr, "\r\nDisconnected from %s.\r\n", ref)
	}()

	// Absorb SIGINT/SIGTERM to prevent bypassing terminal restore.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
		}
	}()

	cleanupWinch := console.HandleSIGWINCH(os.Stdin, pty)
	defer cleanupWinch()

	escapeDisplay := console.FormatEscapeChar(escapeChar)
	fmt.Fprintf(os.Stderr, "Connected to %s (escape sequence: %s.)\r\n", ref, escapeDisplay)

	if err := console.Relay(ctx, pty, escapeChar); err != nil {
		fmt.Fprintf(os.Stderr, "\r\nrelay error: %v\r\n", err)
	}
	return nil
}
