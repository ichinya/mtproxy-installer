package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/version"
)

const logLevelEnv = "MTPROXY_LOG_LEVEL"

type FatalConfigError struct {
	Field   string
	Message string
}

func (e *FatalConfigError) Error() string {
	if e == nil {
		return "fatal configuration error"
	}
	if e.Field == "" {
		return e.Message
	}
	if e.Message == "" {
		return fmt.Sprintf("invalid configuration for %s", e.Field)
	}
	return fmt.Sprintf("invalid configuration for %s: %s", e.Field, e.Message)
}

type commandContext struct {
	Logger  *slog.Logger
	Stdout  io.Writer
	Stderr  io.Writer
	Version version.Info
	Command string
	Args    []string
}

func Execute(args []string, stdout io.Writer, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	versionInfo := version.Current()
	level, err := resolveLogLevel(versionInfo, os.Getenv(logLevelEnv))
	if err != nil {
		fallback := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		fallback.Error("fatal configuration error", "error", redactForCommand("", err.Error()))
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	logger.Info("cli startup",
		"binary", "mtproxy",
		"args_count", len(args),
		"startup_mode", versionInfo.StartupMode(),
	)
	logger.Info("resolved build info",
		"version", versionInfo.Version,
		"commit", versionInfo.Commit,
		"build_date", versionInfo.BuildDate,
		"build_mode", versionInfo.BuildMode,
	)

	commandName, commandArgs := resolveCommand(args)
	logger.Info("selected subcommand", "command", redactForCommand(commandName, commandName))
	logger.Debug("command dispatch start", "command", redactForCommand(commandName, commandName))

	ctx := commandContext{
		Logger:  logger,
		Stdout:  stdout,
		Stderr:  stderr,
		Version: versionInfo,
		Command: commandName,
		Args:    commandArgs,
	}
	if err := routeCommand(ctx); err != nil {
		logCommandError(logger, commandName, err)
		return err
	}

	logger.Debug("command dispatch finish", "command", redactForCommand(commandName, commandName))
	return nil
}

func resolveCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "help", nil
	}

	command := strings.ToLower(strings.TrimSpace(args[0]))
	switch command {
	case "", "help", "-h", "--help":
		return "help", args[1:]
	default:
		return command, args[1:]
	}
}

func routeCommand(ctx commandContext) error {
	switch ctx.Command {
	case "help":
		return runHelp(ctx)
	case "version":
		return runVersion(ctx)
	case "status":
		return runStatus(ctx)
	case "link":
		return runLink(ctx)
	case "install", "update", "uninstall":
		return runPlaceholder(ctx)
	default:
		_ = runHelp(ctx)
		return fmt.Errorf("unknown subcommand: %s", ctx.Command)
	}
}

func runHelp(ctx commandContext) error {
	_, err := fmt.Fprintln(ctx.Stdout, "mtproxy - bootstrap CLI")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "Usage:")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  mtproxy <command>")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "Commands:")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  help      Show this help")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  version   Show build metadata")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  status    Show runtime summary from compose and provider API")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  link      Print proxy link for telemt runtime")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  install   Placeholder command (not implemented)")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  update    Placeholder command (not implemented)")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "  uninstall Placeholder command (not implemented)")
	return err
}

func runVersion(ctx commandContext) error {
	_, err := fmt.Fprintf(
		ctx.Stdout,
		"version=%s commit=%s build_date=%s build_mode=%s\n",
		ctx.Version.Version,
		ctx.Version.Commit,
		ctx.Version.BuildDate,
		ctx.Version.BuildMode,
	)
	return err
}

func runPlaceholder(ctx commandContext) error {
	return fmt.Errorf(
		"subcommand %q is not implemented yet; continue using Bash runtime scripts for operational flows",
		ctx.Command,
	)
}

func resolveLogLevel(info version.Info, raw string) (slog.Level, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if info.IsDevelopment() {
			return slog.LevelDebug, nil
		}
		return slog.LevelInfo, nil
	}

	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, &FatalConfigError{
			Field:   logLevelEnv,
			Message: fmt.Sprintf("unsupported value %q (expected debug, info, warn, error)", raw),
		}
	}
}

func logCommandError(logger *slog.Logger, command string, err error) {
	safeCommand := redactForCommand(command, command)

	var cfgErr *FatalConfigError
	if errors.As(err, &cfgErr) {
		logger.Error(
			"fatal configuration error",
			"command", safeCommand,
			"error", redactForCommand(command, cfgErr.Error()),
		)
		return
	}

	logger.Error(
		"command failed",
		"command", safeCommand,
		"error", redactForCommand(command, err.Error()),
	)
}

func redactForCommand(command string, value string) string {
	_ = command
	return execadapter.RedactText(value)
}
