package cmd

import (
	"context"
	"fmt"
	"runtime"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return initConfig()
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

	cmd.AddCommand(
		pullCmd,
		listCmd,
		dryrunCmd,
		deleteCmd,
		gcCmd,
		runCmd,
		createCmd,
		startCmd,
		stopCmd,
		psCmd,
		inspectCmd,
		consoleCmd,
		rmCmd,
		versionCmd,
	)

	return cmd
}()

func initConfig() error {
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

	return log.SetupLog(context.Background(), &conf.Log, "")
}

// Execute is the main entry point called from main.go.
func Execute() error {
	return rootCmd.Execute()
}
