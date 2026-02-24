package cmd

import (
	"context"
	"fmt"
	"runtime"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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
		Use:   "cocoon",
		Short: "Cocoon - MicroVM Engine",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return initConfig(commandContext(cmd))
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

	confProvider := func() *config.Config { return conf }

	for _, c := range cmdimages.Commands(cmdimages.Handler{ConfProvider: confProvider}) {
		cmd.AddCommand(c)
	}
	for _, c := range cmdvm.Commands(cmdvm.Handler{ConfProvider: confProvider}) {
		cmd.AddCommand(c)
	}
	for _, c := range cmdothers.Commands(cmdothers.Handler{ConfProvider: confProvider}) {
		cmd.AddCommand(c)
	}

	return cmd
}()

func initConfig(ctx context.Context) error {
	conf = config.DefaultConfig()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	}
	_ = viper.ReadInConfig() // optional; missing file is OK

	if err := viper.Unmarshal(conf); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	if conf.PoolSize <= 0 {
		conf.PoolSize = runtime.NumCPU()
	}
	if conf.StopTimeoutSeconds <= 0 {
		conf.StopTimeoutSeconds = 30 //nolint:mnd
	}

	return log.SetupLog(ctx, &conf.Log, "")
}

// Execute is the main entry point called from main.go.
func Execute() error {
	ctx, cancel := newCommandContext()
	defer cancel()
	return rootCmd.ExecuteContext(ctx)
}
