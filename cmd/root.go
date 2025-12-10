package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

var (
	rootCmd = &cobra.Command{
		Use:   "kube-webhook-certgen",
		Short: "Create certificates and patch them to admission hooks",
		Long: `Use this to create a ca and signed certificates and patch admission webhooks to allow for quick
	           installation and configuration of validating and admission webhooks.`,
		PreRunE: configureLogging,
		Run:     rootCommand,
	}

	cfg = struct {
		host               string
		logfmt             string
		secretName         string
		namespace          string
		certName           string
		keyName            string
		logLevel           string
		apiServiceName     string
		webhookName        string
		patchFailurePolicy string
		kubeconfig         string
		patchValidating    bool
		patchMutating      bool
	}{}
)

// Execute is the main entry point for the program.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		slog.Error(err.Error())

		os.Exit(1) //nolint:revive // exit called intentionally
	}
}

func init() {
	rootCmd.Flags()
	rootCmd.PersistentFlags().StringVar(&cfg.logLevel, "log-level", "info", "Log level: panic|fatal|error|warn|info|debug|trace")
	rootCmd.PersistentFlags().StringVar(&cfg.logfmt, "log-format", "json", "Log format: text|json")
	rootCmd.PersistentFlags().StringVar(&cfg.kubeconfig, "kubeconfig", "", "Path to kubeconfig file: e.g. ~/.kube/kind-config-kind")
}

func rootCommand(cmd *cobra.Command, _ []string) {
	_ = cmd.Help()

	os.Exit(0) //nolint:revive // exit called intentionally
}

func newKubernetesClients(kubeconfig string) (kubernetes.Interface, clientset.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("error building kubernetes config: %w", err)
	}

	c, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating kubernetes client: %w", err)
	}

	aggregatorClientset, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating kubernetes aggregator client: %w", err)
	}

	return c, aggregatorClientset, nil
}

func configureLogging(_ *cobra.Command, _ []string) error {
	var level slog.Level

	err := level.UnmarshalText([]byte(cfg.logLevel))
	if err != nil {
		return fmt.Errorf("invalid log level: %w", err)
	}

	handlerOpt := &slog.HandlerOptions{Level: level, AddSource: true}

	var handler slog.Handler

	switch cfg.logfmt {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, handlerOpt) //nolint:forbidigo
	case "text":
		handler = slog.NewTextHandler(os.Stderr, handlerOpt) //nolint:forbidigo
	default:
		return fmt.Errorf("invalid log format: %s", cfg.logfmt)
	}

	slog.SetDefault(slog.New(handler))

	return nil
}
