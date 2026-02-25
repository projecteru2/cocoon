package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	cmdcore "github.com/projecteru2/cocoon/cmd/core"
	cmdimages "github.com/projecteru2/cocoon/cmd/images"
	cmdothers "github.com/projecteru2/cocoon/cmd/others"
	cmdvm "github.com/projecteru2/cocoon/cmd/vm"
	"github.com/projecteru2/cocoon/config"
)

var (
	cfgFile string
	conf    *config.Config
)

var rootCmd = func() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cocoon",
		Short:        "Cocoon - MicroVM Engine",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return initConfig(cmdcore.CommandContext(cmd))
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	cmd.PersistentFlags().String("root-dir", "", "root data directory")
	cmd.PersistentFlags().String("run-dir", "", "runtime directory")
	cmd.PersistentFlags().String("log-dir", "", "log directory")

	_ = viper.BindPFlag("root_dir", cmd.PersistentFlags().Lookup("root-dir"))
	_ = viper.BindPFlag("run_dir", cmd.PersistentFlags().Lookup("run-dir"))
	_ = viper.BindPFlag("log_dir", cmd.PersistentFlags().Lookup("log-dir"))

	viper.SetEnvPrefix("COCOON")
	viper.AutomaticEnv()
	// Explicitly bind keys that have no flag so AutomaticEnv picks up
	// their COCOON_* environment variables during Unmarshal.
	_ = viper.BindEnv("default_root_password")

	confProvider := func() *config.Config { return conf }
	base := cmdcore.BaseHandler{ConfProvider: confProvider}

	cmd.AddCommand(cmdimages.Command(cmdimages.Handler{BaseHandler: base}))
	cmd.AddCommand(cmdvm.Command(cmdvm.Handler{BaseHandler: base}))
	for _, c := range cmdothers.Commands(cmdothers.Handler{BaseHandler: base}) {
		cmd.AddCommand(c)
	}

	return cmd
}()

// Execute is the main entry point called from main.go.
func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return rootCmd.ExecuteContext(ctx)
}

func initConfig(ctx context.Context) error {
	conf = config.DefaultConfig()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	}
	if err := viper.ReadInConfig(); err != nil {
		// No config file is OK; a corrupt/unreadable one is not.
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("read config: %w", err)
		}
	}

	if err := viper.Unmarshal(conf); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	var err error
	conf, err = config.EnsureDirs(conf)
	if err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}
	if conf.PoolSize <= 0 {
		conf.PoolSize = runtime.NumCPU()
	}
	if conf.StopTimeoutSeconds <= 0 {
		conf.StopTimeoutSeconds = 30 //nolint:mnd
	}

	return log.SetupLog(ctx, conf.Log, "")
}
