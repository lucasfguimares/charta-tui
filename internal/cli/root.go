// Package cli defines the Charta command-line interface and composition root.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasfguimares/charta-tui/internal/activity"
	"github.com/lucasfguimares/charta-tui/internal/database"
	"github.com/lucasfguimares/charta-tui/internal/profile"
	"github.com/lucasfguimares/charta-tui/internal/queryhistory"
	"github.com/lucasfguimares/charta-tui/internal/querylibrary"
	"github.com/lucasfguimares/charta-tui/internal/tui"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

const (
	applicationDirectory       = "charta"
	legacyApplicationDirectory = "tui-db"
)

type options struct {
	configPath   string
	logLevel     string
	noKeyring    bool
	historyLimit int
}

// NewCommand builds a fresh Cobra tree so command tests remain isolated.
func NewCommand() *cobra.Command {
	options := options{}
	root := &cobra.Command{
		Use:           "charta",
		Short:         "Interactive SQL database workbench",
		Long:          "Charta manages SQL connections, browses schemas, runs bounded queries, and displays local activity logs.",
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
	root.Flags().IntVar(&options.historyLimit, "history-limit", queryhistory.DefaultLimit, "maximum retained query history entries")
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
				"charta %s\ncommit: %s\nbuilt: %s\n",
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
	if options.historyLimit < 1 || options.historyLimit > queryhistory.MaxLimit {
		return fmt.Errorf("history limit must be between 1 and %d", queryhistory.MaxLimit)
	}
	configPath, logPath, historyPath, libraryPath, err := resolvePaths(options.configPath)
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
	historyStore := queryhistory.NewStore(historyPath, options.historyLimit)
	libraryStore := querylibrary.NewStore(libraryPath)
	model := tui.New(store, secrets, manager, inspector, runner, activityLog, historyStore, libraryStore)

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

func resolvePaths(configPath string) (string, string, string, string, error) {
	usesDefaultConfig := configPath == ""
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", "", "", "", fmt.Errorf("finding user cache directory: %w", err)
	}
	if usesDefaultConfig {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", "", "", "", fmt.Errorf("finding user config directory: %w", err)
		}
		configPath = filepath.Join(configDir, applicationDirectory, "connections.json")
		if err := migrateLegacyData(configDir, cacheDir); err != nil {
			return "", "", "", "", err
		}
	}
	dataDir := filepath.Dir(configPath)
	return configPath,
		filepath.Join(cacheDir, applicationDirectory, "activity.jsonl"),
		filepath.Join(dataDir, "query-history.json"),
		filepath.Join(dataDir, "sql-library.json"), nil
}

func migrateLegacyData(configDir, cacheDir string) error {
	pairs := [][2]string{
		{filepath.Join(configDir, legacyApplicationDirectory, "connections.json"), filepath.Join(configDir, applicationDirectory, "connections.json")},
		{filepath.Join(configDir, legacyApplicationDirectory, "query-history.json"), filepath.Join(configDir, applicationDirectory, "query-history.json")},
		{filepath.Join(configDir, legacyApplicationDirectory, "sql-library.json"), filepath.Join(configDir, applicationDirectory, "sql-library.json")},
		{filepath.Join(cacheDir, legacyApplicationDirectory, "activity.jsonl"), filepath.Join(cacheDir, applicationDirectory, "activity.jsonl")},
	}
	for _, pair := range pairs {
		if err := migrateLegacyFile(pair[0], pair[1]); err != nil {
			return fmt.Errorf("migrating Charta data: %w", err)
		}
	}
	return nil
}

func migrateLegacyFile(source, destination string) error {
	if _, err := os.Stat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking destination %q: %w", destination, err)
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("checking legacy file %q: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("moving %q to %q: %w", source, destination, err)
	}
	return nil
}
