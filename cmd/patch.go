package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jkroepke/kube-webhook-certgen/pkg/k8s"
	"github.com/spf13/cobra"
	admissionv1 "k8s.io/api/admissionregistration/v1"
)

var patch = &cobra.Command{
	Use:     "patch",
	Short:   "Patch a ValidatingWebhookConfiguration, MutatingWebhookConfiguration or APIService 'object-name' by using the ca from 'secret-name' in 'namespace'",
	Long:    "Patch a ValidatingWebhookConfiguration, MutatingWebhookConfiguration or APIService 'object-name' by using the ca from 'secret-name' in 'namespace'",
	PreRunE: configureLogging,
	RunE:    patchCommand,
}

type PatchConfig struct {
	Patcher            Patcher
	PatchFailurePolicy string
	APIServiceName     string
	WebhookName        string
	SecretName         string
	CaName             string
	Namespace          string
	PatchMethod        string
	PatchMutating      bool
	PatchValidating    bool
}

type Patcher interface {
	PatchObjects(ctx context.Context, options k8s.PatchOptions) error
	GetCaFromSecret(ctx context.Context, caName, secretName, namespace string) ([]byte, error)
}

//nolint:cyclop
func Patch(ctx context.Context, cfg *PatchConfig) error {
	if cfg.Patcher == nil {
		return errors.New("no patcher defined")
	}

	if !cfg.PatchMutating && !cfg.PatchValidating && cfg.APIServiceName == "" {
		return errors.New("patch-validating=false, patch-mutating=false. You must patch at least one kind of webhook, otherwise this command is a no-op")
	}

	var failurePolicy admissionv1.FailurePolicyType

	switch cfg.PatchFailurePolicy {
	case "":
	case "Ignore":
	case "Fail":
		failurePolicy = admissionv1.FailurePolicyType(cfg.PatchFailurePolicy)
	default:
		return fmt.Errorf("patch-failure-policy %s is not valid", cfg.PatchFailurePolicy)
	}

	ca, err := cfg.Patcher.GetCaFromSecret(ctx, cfg.CaName, cfg.SecretName, cfg.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get ca from secret '%s' in namespace '%s': %w", cfg.SecretName, cfg.Namespace, err)
	}

	if ca == nil {
		return fmt.Errorf("no secret with '%s' in '%s'", cfg.SecretName, cfg.Namespace)
	}

	options := k8s.PatchOptions{
		CABundle:          ca,
		FailurePolicyType: failurePolicy,
		APIServiceName:    cfg.APIServiceName,
		PatchMethod:       cfg.PatchMethod,
	}

	if cfg.PatchMutating {
		options.MutatingWebhookConfigurationName = cfg.WebhookName
	}

	if cfg.PatchValidating {
		options.ValidatingWebhookConfigurationName = cfg.WebhookName
	}

	if err := cfg.Patcher.PatchObjects(ctx, options); err != nil {
		return fmt.Errorf("failed to patch objects: %w", err)
	}

	return nil
}

func patchCommand(_ *cobra.Command, _ []string) error {
	client, aggregationClient, err := newKubernetesClients(cfg.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	patcher, err := k8s.New(client, aggregationClient)
	if err != nil {
		return fmt.Errorf("failed to create patcher: %w", err)
	}

	config := &PatchConfig{
		SecretName:         cfg.secretName,
		CaName:             cfg.caName,
		Namespace:          cfg.namespace,
		PatchMutating:      cfg.patchMutating,
		PatchValidating:    cfg.patchValidating,
		PatchFailurePolicy: cfg.patchFailurePolicy,
		APIServiceName:     cfg.apiServiceName,
		WebhookName:        cfg.webhookName,
		PatchMethod:        cfg.patchMethod,
		Patcher:            patcher,
	}

	if err := Patch(context.Background(), config); err != nil {
		if wrappedErr := errors.Unwrap(err); wrappedErr != nil {
			err = wrappedErr
		}

		return fmt.Errorf("failed to patch webhooks: %w", err)
	}

	slog.Info("successfully patched webhooks")

	return nil
}

//nolint:lll
func init() {
	rootCmd.AddCommand(patch)
	patch.Flags().StringVar(&cfg.secretName, "secret-name", "", "Name of the secret where certificate information will be read from")
	patch.Flags().StringVar(&cfg.secretType, "secret-type", "", "Name of the secret where certificate information will be read from")
	patch.Flags().StringVar(&cfg.namespace, "namespace", "", "Namespace of the secret where certificate information will be read from")
	patch.Flags().StringVar(&cfg.webhookName, "webhook-name", "", "Name of ValidatingWebhookConfiguration and MutatingWebhookConfiguration that will be updated")
	patch.Flags().StringVar(&cfg.apiServiceName, "apiservice-name", "", "Name of APIService that will be patched")
	patch.Flags().StringVar(&cfg.caName, "ca-name", "ca", "Name of cert file in the secret")
	patch.Flags().StringVar(&cfg.patchMethod, "patch-mode", "update", "Patch method to use: patch|update. patch uses server side apply, update uses a full object update")
	patch.Flags().BoolVar(&cfg.patchValidating, "patch-validating", true, "If true, patch ValidatingWebhookConfiguration")
	patch.Flags().BoolVar(&cfg.patchMutating, "patch-mutating", true, "If true, patch MutatingWebhookConfiguration")
	patch.Flags().StringVar(&cfg.patchFailurePolicy, "patch-failure-policy", "", "If set, patch the webhooks with this failure policy. Valid options are Ignore or Fail")

	_ = patch.MarkFlagRequired("secret-name")
	_ = patch.MarkFlagRequired("namespace")
}
