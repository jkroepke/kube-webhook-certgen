package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	admissionapplyv1 "k8s.io/client-go/applyconfigurations/admissionregistration/v1"
	meta "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

// K8s provides methods to interact with Kubernetes resources for certificate management.
type K8s struct {
	clientSet           kubernetes.Interface
	aggregatorClientSet clientset.Interface
}

// New creates a new K8s instance with the provided client sets.
func New(clientSet kubernetes.Interface, aggregatorClientSet clientset.Interface) (*K8s, error) {
	if clientSet == nil {
		return nil, errors.New("no kubernetes client given")
	}

	if aggregatorClientSet == nil {
		return nil, errors.New("no kubernetes aggregator client given")
	}

	return &K8s{
		clientSet:           clientSet,
		aggregatorClientSet: aggregatorClientSet,
	}, nil
}

// PatchOptions contains configuration for patching webhook configurations and API services.
type PatchOptions struct {
	ValidatingWebhookConfigurationName string
	MutatingWebhookConfigurationName   string
	APIServiceName                     string
	PatchMethod                        string // Either "patch" or "update"
	FailurePolicyType                  admissionregistrationv1.FailurePolicyType
	CABundle                           []byte
}

// ErrNoSecret is returned when a secret is not found.
var ErrNoSecret = errors.New("no secret found")

// GetCaFromSecret retrieves the CA certificate from a Kubernetes secret.
// Returns ErrNoSecret if the secret doesn't exist, or an error if the secret
// exists but doesn't contain a 'ca.crt' key.
func (k *K8s) GetCaFromSecret(ctx context.Context, secretName, namespace string) ([]byte, error) {
	slog.DebugContext(ctx, "getting CA from secret",
		slog.String("secret", secretName),
		slog.String("namespace", namespace),
	)

	secret, err := k.clientSet.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, ErrNoSecret
		}

		return nil, fmt.Errorf("error getting secret: %w", err)
	}

	data := secret.Data["ca.crt"]
	if data == nil {
		return nil, errors.New("got secret, but it did not contain a 'ca.crt' key")
	}

	return data, nil
}

// PatchObjects patches webhook configurations and/or API services with the provided CA bundle.
// It validates the patch options and then patches the specified resources.
func (k *K8s) PatchObjects(ctx context.Context, options PatchOptions) error {
	if err := validatePatchOptions(options); err != nil {
		return err
	}

	// Patch API service if specified
	if options.APIServiceName != "" {
		if err := k.patchAPIService(ctx, options.APIServiceName, options.CABundle); err != nil {
			return err
		}
	}

	// Patch webhook configurations if specified
	shouldPatchWebhooks := options.MutatingWebhookConfigurationName != "" ||
		options.ValidatingWebhookConfigurationName != ""

	if shouldPatchWebhooks {
		webhookName := getWebhookName(options)
		patchMutating := options.MutatingWebhookConfigurationName != ""
		patchValidating := options.ValidatingWebhookConfigurationName != ""

		return k.patchWebhookConfigurations(
			ctx,
			webhookName,
			options.CABundle,
			options.FailurePolicyType,
			patchMutating,
			patchValidating,
			options.PatchMethod,
		)
	}

	return nil
}

// validatePatchOptions validates the patch options before applying them.
func validatePatchOptions(options PatchOptions) error {
	validPatchMethods := map[string]bool{"patch": true, "update": true}
	if !validPatchMethods[options.PatchMethod] {
		return fmt.Errorf("invalid patch method '%s', must be 'patch' or 'update'", options.PatchMethod)
	}

	hasMutating := options.MutatingWebhookConfigurationName != ""
	hasValidating := options.ValidatingWebhookConfigurationName != ""
	hasFailurePolicy := options.FailurePolicyType != ""

	// Failure policy is only valid when patching webhooks
	if hasFailurePolicy && !hasMutating && !hasValidating {
		return errors.New("failurePolicy specified, but no webhook will be patched")
	}

	// If both webhooks are specified, they must have the same name
	if hasMutating && hasValidating &&
		options.MutatingWebhookConfigurationName != options.ValidatingWebhookConfigurationName {
		return errors.New("mutating and validating webhook names must be the same")
	}

	return nil
}

// getWebhookName returns the webhook configuration name to use for patching.
func getWebhookName(options PatchOptions) string {
	if options.ValidatingWebhookConfigurationName != "" {
		return options.ValidatingWebhookConfigurationName
	}

	return options.MutatingWebhookConfigurationName
}

// SaveCertsToSecret saves the provided CA, certificate and key into a secret in the specified namespace.
//
//nolint:revive
func (k *K8s) SaveCertsToSecret(ctx context.Context, secretName, secretType, namespace, certName, keyName string, ca, cert, key []byte) error {
	slog.DebugContext(ctx, "saving certificates to secret",
		slog.String("secret", secretName),
		slog.String("namespace", namespace),
	)

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Type: v1.SecretType(secretType),
		Data: map[string][]byte{
			"ca.crt": ca,
			certName: cert,
			keyName:  key,
		},
	}

	_, err := k.clientSet.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating secret: %w", err)
	}

	slog.DebugContext(ctx, "successfully saved secret")

	return nil
}

func (k *K8s) patchAPIService(ctx context.Context, objectName string, ca []byte) error {
	slog.InfoContext(ctx, "patching APIService",
		slog.String("api_service", objectName),
	)

	client := k.aggregatorClientSet.ApiregistrationV1().APIServices()

	apiService, err := client.Get(ctx, objectName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting APIService: %w", err)
	}

	apiService.Spec.CABundle = ca
	apiService.Spec.InsecureSkipTLSVerify = false

	if _, err := client.Update(ctx, apiService, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error patching APIService: %w", err)
	}

	slog.DebugContext(ctx, "successfully patched APIService")

	return nil
}

// patchWebhookConfigurations patches webhook configurations with CA bundle and optional failure policy.
func (k *K8s) patchWebhookConfigurations(
	ctx context.Context,
	configurationName string,
	ca []byte,
	failurePolicy admissionregistrationv1.FailurePolicyType,
	patchMutating,
	patchValidating bool,
	patchMethod string,
) error {
	slog.InfoContext(ctx, "patching webhook configurations",
		slog.String("configuration_name", configurationName),
		slog.Bool("patch_mutating", patchMutating),
		slog.Bool("patch_validating", patchValidating),
		slog.String("failure_policy", string(failurePolicy)),
	)

	if patchValidating {
		if err := k.patchValidatingWebhook(ctx, configurationName, ca, failurePolicy, patchMethod); err != nil {
			return err
		}
	} else {
		slog.DebugContext(ctx, "validating hook patching not required")
	}

	if patchMutating {
		if err := k.patchMutatingWebhook(ctx, configurationName, ca, failurePolicy, patchMethod); err != nil {
			return err
		}
	} else {
		slog.DebugContext(ctx, "mutating hook patching not required")
	}

	slog.InfoContext(ctx, "successfully patched webhook configuration(s)")

	return nil
}

// patchValidatingWebhook patches a validating webhook with the specified method (patch or update).
func (k *K8s) patchValidatingWebhook(
	ctx context.Context,
	configurationName string,
	ca []byte,
	failurePolicy admissionregistrationv1.FailurePolicyType,
	patchMethod string,
) error {
	if patchMethod == "update" {
		if err := k.updateValidatingWebhook(ctx, configurationName, ca, failurePolicy); err != nil {
			return err
		}
	}

	return k.applyValidatingWebhook(ctx, configurationName, ca, failurePolicy)
}

// patchMutatingWebhook patches a mutating webhook with the specified method (patch or update).
func (k *K8s) patchMutatingWebhook(
	ctx context.Context,
	configurationName string,
	ca []byte,
	failurePolicy admissionregistrationv1.FailurePolicyType,
	patchMethod string,
) error {
	if patchMethod == "update" {
		if err := k.updateMutatingWebhook(ctx, configurationName, ca, failurePolicy); err != nil {
			return err
		}
	}

	return k.applyMutatingWebhook(ctx, configurationName, ca, failurePolicy)
}

func (k *K8s) applyValidatingWebhook(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	valHook, err := k.clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting validating webhook: %w", err)
	}

	applyConfig := &admissionapplyv1.ValidatingWebhookConfigurationApplyConfiguration{
		TypeMetaApplyConfiguration: meta.TypeMetaApplyConfiguration{
			Kind:       ptr("ValidatingWebhookConfiguration"),
			APIVersion: ptr("admissionregistration.k8s.io/v1"),
		},
		ObjectMetaApplyConfiguration: &meta.ObjectMetaApplyConfiguration{
			Name: &configurationName,
		},
		Webhooks: make([]admissionapplyv1.ValidatingWebhookApplyConfiguration, 0, len(valHook.Webhooks)),
	}

	for i := range valHook.Webhooks {
		config := admissionapplyv1.ValidatingWebhookApplyConfiguration{
			Name: &valHook.Webhooks[i].Name,
			ClientConfig: &admissionapplyv1.WebhookClientConfigApplyConfiguration{
				CABundle: ca,
			},
		}

		if failurePolicy != "" {
			config.FailurePolicy = &failurePolicy
		}

		applyConfig.Webhooks = append(applyConfig.Webhooks, config)
	}

	if _, err = k.clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Apply(ctx, applyConfig, metav1.ApplyOptions{
		FieldManager: "kube-webhook-certgen",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed patching validating webhook: %w", err)
	}

	slog.DebugContext(ctx, "successfully applied validating webhook configuration")

	return nil
}

func (k *K8s) updateValidatingWebhook(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	valHook, err := k.clientSet.
		AdmissionregistrationV1().
		ValidatingWebhookConfigurations().
		Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting validating webhook: %w", err)
	}

	for i := range valHook.Webhooks {
		h := &valHook.Webhooks[i]

		h.ClientConfig.CABundle = ca
		if failurePolicy != "" {
			h.FailurePolicy = &failurePolicy
		}
	}

	if _, err = k.clientSet.AdmissionregistrationV1().
		ValidatingWebhookConfigurations().
		Update(ctx, valHook, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed patching validating webhook: %w", err)
	}

	slog.DebugContext(ctx, "successfully updated validating webhook configuration")

	return nil
}

func (k *K8s) applyMutatingWebhook(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	mutHook, err := k.clientSet.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting mutating webhook: %w", err)
	}

	applyConfig := &admissionapplyv1.MutatingWebhookConfigurationApplyConfiguration{
		TypeMetaApplyConfiguration: meta.TypeMetaApplyConfiguration{
			Kind:       ptr("MutatingWebhookConfiguration"),
			APIVersion: ptr("admissionregistration.k8s.io/v1"),
		},
		ObjectMetaApplyConfiguration: &meta.ObjectMetaApplyConfiguration{
			Name: &configurationName,
		},
		Webhooks: make([]admissionapplyv1.MutatingWebhookApplyConfiguration, 0, len(mutHook.Webhooks)),
	}

	for i := range mutHook.Webhooks {
		config := admissionapplyv1.MutatingWebhookApplyConfiguration{
			Name: &mutHook.Webhooks[i].Name,
			ClientConfig: &admissionapplyv1.WebhookClientConfigApplyConfiguration{
				CABundle: ca,
			},
		}

		if failurePolicy != "" {
			config.FailurePolicy = &failurePolicy
		}

		applyConfig.Webhooks = append(applyConfig.Webhooks, config)
	}

	if _, err = k.clientSet.AdmissionregistrationV1().MutatingWebhookConfigurations().Apply(ctx, applyConfig, metav1.ApplyOptions{
		FieldManager: "kube-webhook-certgen",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed patching mutating webhook: %w", err)
	}

	slog.DebugContext(ctx, "successfully applied mutating webhook configuration")

	return nil
}

func (k *K8s) updateMutatingWebhook(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	mutHook, err := k.clientSet.
		AdmissionregistrationV1().
		MutatingWebhookConfigurations().
		Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting mutating webhook: %w", err)
	}

	for i := range mutHook.Webhooks {
		h := &mutHook.Webhooks[i]

		h.ClientConfig.CABundle = ca
		if failurePolicy != "" {
			h.FailurePolicy = &failurePolicy
		}
	}

	if _, err = k.clientSet.AdmissionregistrationV1().
		MutatingWebhookConfigurations().
		Update(ctx, mutHook, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed patching mutating webhook: %w", err)
	}

	slog.DebugContext(ctx, "successfully updated mutating webhook configuration")

	return nil
}

func ptr(s string) *string {
	return &s
}
