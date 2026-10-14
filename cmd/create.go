package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jkroepke/kube-webhook-certgen/pkg/certs"
	"github.com/jkroepke/kube-webhook-certgen/pkg/k8s"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
)

var create = &cobra.Command{
	Use:     "create",
	Short:   "Generate a ca and server cert+key and store the results in a secret 'secret-name' in 'namespace'",
	Long:    "Generate a ca and server cert+key and store the results in a secret 'secret-name' in 'namespace'",
	PreRunE: configureLogging,
	RunE:    createCommand,
}

func createCommand(cmd *cobra.Command, _ []string) error {
	clientSet, aggregatorClientSet, err := newKubernetesClients(cfg.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	k, err := k8s.New(clientSet, aggregatorClientSet)
	if err != nil {
		return fmt.Errorf("failed to create k8s helper: %w", err)
	}

	if cfg.secretType == string(v1.SecretTypeTLS) {
		setTLSSecretKeyNames(cmd)
	}

	return ensureSecret(context.TODO(), k)
}

// ensureSecret creates the secret with new certificates if it does not exist.
// An existing secret is migrated if kubernetes.io/tls is requested, but the secret is of another type.
func ensureSecret(ctx context.Context, k *k8s.K8s) error {
	secret, err := k.GetSecret(ctx, cfg.secretName, cfg.namespace)
	switch {
	case errors.Is(err, k8s.ErrNoSecret):
		slog.InfoContext(ctx, "creating new secret")

		newCa, newCert, newKey, err := certs.GenerateCerts(cfg.host)
		if err != nil {
			return fmt.Errorf("failed to generate certs: %w", err)
		}

		err = k.SaveCertsToSecret(ctx, cfg.secretName, cfg.secretType, cfg.namespace, cfg.caName, cfg.certName, cfg.keyName, newCa, newCert, newKey)
		if err != nil {
			return fmt.Errorf("failed to save certs to secret: %w", err)
		}
	case err != nil:
		return fmt.Errorf("failed to get secret: %w", err)
	case cfg.secretType == string(v1.SecretTypeTLS) && secret.Type != v1.SecretTypeTLS:
		if err := k.MigrateSecretToTLS(ctx, secret, cfg.caName, cfg.certName, cfg.keyName); err != nil {
			return fmt.Errorf("failed to migrate secret: %w", err)
		}
	default:
		slog.InfoContext(ctx, "secret already exists")
	}

	return nil
}

// setTLSSecretKeyNames defaults the key names to the ones of kubernetes.io/tls secrets, unless set explicitly.
func setTLSSecretKeyNames(cmd *cobra.Command) {
	if !cmd.Flags().Changed("ca-name") {
		cfg.caName = v1.ServiceAccountRootCAKey
	}

	if !cmd.Flags().Changed("cert-name") {
		cfg.certName = v1.TLSCertKey
	}

	if !cmd.Flags().Changed("key-name") {
		cfg.keyName = v1.TLSPrivateKeyKey
	}
}

//nolint:lll
func init() {
	rootCmd.AddCommand(create)
	create.Flags().StringVar(&cfg.host, "host", "", "Comma-separated hostnames and IPs to generate a certificate for")
	create.Flags().StringVar(&cfg.secretName, "secret-name", "", "Name of the secret where certificate information will be written")
	create.Flags().StringVar(&cfg.secretType, "secret-type", "Opaque", "Type of the secret where certificate information will be written. Set to kubernetes.io/tls to use tls.crt, tls.key and ca.crt as default key names and to migrate an existing secret of another type")
	create.Flags().StringVar(&cfg.namespace, "namespace", "", "Namespace of the secret where certificate information will be written")
	create.Flags().StringVar(&cfg.caName, "ca-name", "ca", "Name of ca file in the secret. Defaults to ca.crt for kubernetes.io/tls secrets")
	create.Flags().StringVar(&cfg.certName, "cert-name", "cert", "Name of cert file in the secret. Defaults to tls.crt for kubernetes.io/tls secrets")
	create.Flags().StringVar(&cfg.keyName, "key-name", "key", "Name of key file in the secret. Defaults to tls.key for kubernetes.io/tls secrets")

	_ = create.MarkFlagRequired("host")
	_ = create.MarkFlagRequired("secret-name")
	_ = create.MarkFlagRequired("namespace")
}
