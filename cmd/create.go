package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jkroepke/kube-webhook-certgen/pkg/certs"
	"github.com/jkroepke/kube-webhook-certgen/pkg/k8s"
	"github.com/spf13/cobra"
)

var create = &cobra.Command{
	Use:     "create",
	Short:   "Generate a ca and server cert+key and store the results in a secret 'secret-name' in 'namespace'",
	Long:    "Generate a ca and server cert+key and store the results in a secret 'secret-name' in 'namespace'",
	PreRunE: configureLogging,
	RunE:    createCommand,
}

func createCommand(_ *cobra.Command, _ []string) error {
	clientSet, aggregatorClientSet, err := newKubernetesClients(cfg.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	k, err := k8s.New(clientSet, aggregatorClientSet)
	if err != nil {
		slog.Error("failed to create k8s helper", slog.Any("err", err))
	}

	ctx := context.TODO()

	_, err = k.GetCaFromSecret(ctx, cfg.secretName, cfg.namespace)
	switch {
	case errors.Is(err, k8s.ErrNoSecret):
		slog.Info("creating new secret")

		newCa, newCert, newKey, err := certs.GenerateCerts(cfg.host)
		if err != nil {
			return fmt.Errorf("failed to generate certs: %w", err)
		}

		err = k.SaveCertsToSecret(ctx, cfg.secretName, cfg.secretType, cfg.namespace, cfg.certName, cfg.keyName, newCa, newCert, newKey)
		if err != nil {
			return fmt.Errorf("failed to save certs to secret: %w", err)
		}
	case err != nil:
		return fmt.Errorf("failed to get secret: %w", err)
	default:
		slog.Info("secret already exists")
	}

	return nil
}

func init() {
	rootCmd.AddCommand(create)
	create.Flags().StringVar(&cfg.host, "host", "", "Comma-separated hostnames and IPs to generate a certificate for")
	create.Flags().StringVar(&cfg.secretName, "secret-name", "", "Name of the secret where certificate information will be written")
	create.Flags().StringVar(&cfg.secretType, "secret-type", "Opaque", "Type of the secret where certificate information will be written")
	create.Flags().StringVar(&cfg.namespace, "namespace", "", "Namespace of the secret where certificate information will be written")
	create.Flags().StringVar(&cfg.certName, "cert-name", "cert", "Name of cert file in the secret")
	create.Flags().StringVar(&cfg.keyName, "key-name", "key", "Name of key file in the secret")

	_ = create.MarkFlagRequired("host")
	_ = create.MarkFlagRequired("secret-name")
	_ = create.MarkFlagRequired("namespace")
}
