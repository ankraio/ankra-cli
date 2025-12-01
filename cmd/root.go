package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	apiToken string
	baseURL  string
	cfgFile  string
	version  = "0.1.121"
)

var rootCmd = &cobra.Command{
	Use:     "ankra",
	Short:   "CLI for the Ankra platform",
	Version: version,
	Long: `Ankra CLI allows you to manage clusters, operations,
addons, persistent selection, and more.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().
		StringVar(&cfgFile, "config", "", "config file (default is $HOME/.ankra.yaml)")

	rootCmd.PersistentFlags().
		String("token", "", "API token for authentication (or set ANKRA_API_TOKEN)")
	rootCmd.PersistentFlags().
		String("base-url", "https://platform.ankra.app", "Base URL for the Ankra API (or set ANKRA_BASE_URL)")

	_ = viper.BindPFlag("token", rootCmd.PersistentFlags().Lookup("token"))
	_ = viper.BindPFlag("base-url", rootCmd.PersistentFlags().Lookup("base-url"))
	_ = viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))

	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(selectCmd)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else if home, err := os.UserHomeDir(); err == nil {
		viper.AddConfigPath(home)
		viper.SetConfigName(".ankra")
		viper.SetConfigType("yaml")
	}
	viper.AutomaticEnv()
	if err := viper.BindEnv("token", "ANKRA_API_TOKEN"); err != nil {
		fmt.Printf("Warning: Could not bind token environment variable: %v\n", err)
	}
	if err := viper.BindEnv("base-url", "ANKRA_BASE_URL"); err != nil {
		fmt.Printf("Warning: Could not bind base-url environment variable: %v\n", err)
	}
	if err := viper.ReadInConfig(); err == nil {
		viper.ConfigFileUsed()
	}

	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		return
	}

	if viper.GetString("token") == "" {
		fmt.Fprintln(os.Stderr,
			"API token not provided; use --token, ANKRA_API_TOKEN, or `ankra login`")
		os.Exit(1)
	}
	apiToken = viper.GetString("token")
	baseURL = viper.GetString("base-url")
}
