// Package cli defines the tui-db command-line interface and composition root.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/tui-db/internal/activity"
	"github.com/lucasfguimares/tui-db/internal/database"
	"github.com/lucasfguimares/tui-db/internal/profile"
	"github.com/lucasfguimares/tui-db/internal/tui"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type options struct {
	configPath string
	logLevel   string
	noKeyring  bool
}

// NewCommand builds a fresh Cobra tree so command tests remain isolated.
func NewCommand() *cobra.Command {
	options := options{}
	root := &cobra.Command{
		Use:           "tui-db",
		Short:         "Interactive SQL database workbench",
		Long:          "tui-db manages SQL connections, browses schemas, runs bounded queries, and displays local activity logs.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), options)
		},
	}
	root.Flags().StringVar(&options.configPath, "config", "", "connection profile JSON path")
	root.Flags().StringVar(&options.logLevel, "log-level", "info", "activity log level (debug, info, warn, error)")
	root.Flags().BoolVar(&options.noKeyring, "no-keyring", false, "keep passwords in memory for this session only")
	root.AddCommand(newVersionCommand())
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"tui-db %s\ncommit: %s\nbuilt: %s\n",
				version,
				commit,
				buildDate,
			)
			if err != nil {
				return fmt.Errorf("writing version: %w", err)
			}
			return nil
		},
	}
}

func run(ctx context.Context, options options) error {
	level, err := activity.ParseLevel(options.logLevel)
	if err != nil {
		return err
	}
	configPath, logPath, err := resolvePaths(options.configPath)
	if err != nil {
		return err
	}
	activityLog, err := activity.Open(logPath, level)
	if err != nil {
		return err
	}

	var secrets profile.SecretStore = profile.NewMemoryStore()
	if !options.noKeyring {
		secrets = profile.NewHybridStore(profile.NewKeyringStore())
	}
	store := profile.NewStore(configPath)
	manager := database.NewManager(secrets)
	inspector := database.NewInspector(manager)
	runner := database.NewRunner(manager)
	model := tui.New(store, secrets, manager, inspector, runner, activityLog)

	activityLog.Record(ctx, activity.Event{Level: slog.LevelInfo, Message: "application started"})
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
	)
	_, runErr := program.Run()
	activityLog.Record(context.WithoutCancel(ctx), activity.Event{
		Level: slog.LevelInfo, Message: "application stopped", Error: runErr,
	})
	closeErr := errors.Join(manager.Close(), activityLog.Close())
	if runErr != nil {
		return fmt.Errorf("running tui: %w", runErr)
	}
	return closeErr
}

func resolvePaths(configPath string) (string, string, error) {
	if configPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", "", fmt.Errorf("finding user config directory: %w", err)
		}
		configPath = filepath.Join(configDir, "tui-db", "connections.json")
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", "", fmt.Errorf("finding user cache directory: %w", err)
	}
	return configPath, filepath.Join(cacheDir, "tui-db", "activity.jsonl"), nil
}
