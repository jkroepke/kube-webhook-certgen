package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	admissionapplyv1 "k8s.io/client-go/applyconfigurations/admissionregistration/v1"
	meta "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	apiregistrationapplyv1 "k8s.io/kube-aggregator/pkg/client/applyconfiguration/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

// fieldManager is the server-side apply field manager used for all patched objects.
const fieldManager = "kube-webhook-certgen"

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
	FailurePolicyType                  admissionregistrationv1.FailurePolicyType
	CABundle                           []byte
}

// ErrNoSecret is returned when a secret is not found.
var ErrNoSecret = errors.New("no secret found")

// Legacy key names used by secrets created with the default flags of the Opaque secret type.
const (
	legacyCAKey   = "ca"
	legacyCertKey = "cert"
	legacyKeyKey  = "key"
)

// GetSecret retrieves a Kubernetes secret. Returns ErrNoSecret if the secret doesn't exist.
func (k *K8s) GetSecret(ctx context.Context, secretName, namespace string) (*v1.Secret, error) {
	secret, err := k.clientSet.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, ErrNoSecret
		}

		return nil, fmt.Errorf("error getting secret: %w", err)
	}

	return secret, nil
}

// GetCaFromSecret retrieves the CA certificate from a Kubernetes secret.
// Returns ErrNoSecret if the secret doesn't exist, or an error if the secret
// exists but contains neither the caName key nor one of the 'ca.crt' or 'ca' fallback keys.
func (k *K8s) GetCaFromSecret(ctx context.Context, caName, secretName, namespace string) ([]byte, error) {
	slog.DebugContext(ctx, "getting CA from secret",
		slog.String("secret", secretName),
		slog.String("namespace", namespace),
	)

	secret, err := k.GetSecret(ctx, secretName, namespace)
	if err != nil {
		return nil, err
	}

	// Fallback to 'ca.crt' (kubernetes.io/tls) and 'ca' (Opaque) for backward compatibility
	for _, name := range []string{caName, v1.ServiceAccountRootCAKey, legacyCAKey} {
		if data := secret.Data[name]; data != nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("got secret, but it did not contain a '%s' key", caName)
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
		)
	}

	return nil
}

// validatePatchOptions validates the patch options before applying them.
func validatePatchOptions(options PatchOptions) error {
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
func (k *K8s) SaveCertsToSecret(ctx context.Context, secretName, secretType, namespace, caName, certName, keyName string, ca, cert, key []byte) error {
	slog.DebugContext(ctx, "saving certificates to secret",
		slog.String("secret", secretName),
		slog.String("namespace", namespace),
	)

	secret := &v1.Secret{
		Name: secretName,
		Type: v1.SecretType(secretType),
		Data: map[string][]byte{
			caName:   ca,
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

// MigrateSecretToTLS converts an existing secret into a kubernetes.io/tls secret.
//
// The type of secret is immutable, so the secret is deleted and recreated with the same certificates.
// Missing caName, certName and keyName entries are copied from the legacy 'ca', 'cert' and 'key' entries.
// All existing entries are kept, so workloads that still mount the legacy entries keep working.
func (k *K8s) MigrateSecretToTLS(ctx context.Context, secret *v1.Secret, caName, certName, keyName string) error {
	slog.InfoContext(ctx, "migrating secret to type "+string(v1.SecretTypeTLS),
		slog.String("secret", secret.Name),
		slog.String("namespace", secret.Namespace),
		slog.String("type", string(secret.Type)),
	)

	data := maps.Clone(secret.Data)
	if data == nil {
		data = make(map[string][]byte, 3)
	}

	for _, entry := range []struct{ name, legacyName string }{
		{caName, legacyCAKey},
		{certName, legacyCertKey},
		{keyName, legacyKeyKey},
	} {
		if _, ok := data[entry.name]; ok {
			continue
		}

		value, ok := secret.Data[entry.legacyName]
		if !ok {
			return fmt.Errorf("unable to migrate secret: neither '%s' nor '%s' key found", entry.name, entry.legacyName)
		}

		data[entry.name] = value
	}

	// Validate before deleting the secret, the API server would reject the new secret otherwise.
	for _, required := range []string{v1.TLSCertKey, v1.TLSPrivateKeyKey} {
		if _, ok := data[required]; !ok {
			return fmt.Errorf("unable to migrate secret: type %s requires a '%s' key", v1.SecretTypeTLS, required)
		}
	}

	migrated := &v1.Secret{
		Name:            secret.Name,
		Namespace:       secret.Namespace,
		Labels:          secret.Labels,
		Annotations:     secret.Annotations,
		OwnerReferences: secret.OwnerReferences,
		Type:            v1.SecretTypeTLS,
		Data:            data,
	}

	client := k.clientSet.CoreV1().Secrets(secret.Namespace)

	// Only delete the secret if it was not modified in the meantime.
	if err := client.Delete(ctx, secret.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID:             &secret.UID,
			ResourceVersion: &secret.ResourceVersion,
		},
	}); err != nil {
		return fmt.Errorf("error deleting secret: %w", err)
	}

	if _, err := client.Create(ctx, migrated, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("error recreating secret as type %s: %w", v1.SecretTypeTLS, err)
	}

	slog.InfoContext(ctx, "successfully migrated secret")

	return nil
}

func (k *K8s) patchAPIService(ctx context.Context, objectName string, ca []byte) error {
	slog.InfoContext(ctx, "patching APIService",
		slog.String("api_service", objectName),
	)

	client := k.aggregatorClientSet.ApiregistrationV1().APIServices()

	// Ensure the APIService exists, server-side apply would create it otherwise.
	if _, err := client.Get(ctx, objectName, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("error getting APIService: %w", err)
	}

	applyConfig := apiregistrationapplyv1.APIService(objectName).
		WithSpec(apiregistrationapplyv1.APIServiceSpec().
			WithCABundle(ca...).
			WithInsecureSkipTLSVerify(false),
		)

	if _, err := client.Apply(ctx, applyConfig, metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true,
	}); err != nil {
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
) error {
	slog.InfoContext(ctx, "patching webhook configurations",
		slog.String("configuration_name", configurationName),
		slog.Bool("patch_mutating", patchMutating),
		slog.Bool("patch_validating", patchValidating),
		slog.String("failure_policy", string(failurePolicy)),
	)

	if patchValidating {
		if err := k.applyValidatingWebhook(ctx, configurationName, ca, failurePolicy); err != nil {
			return err
		}
	} else {
		slog.DebugContext(ctx, "validating hook patching not required")
	}

	if patchMutating {
		if err := k.applyMutatingWebhook(ctx, configurationName, ca, failurePolicy); err != nil {
			return err
		}
	} else {
		slog.DebugContext(ctx, "mutating hook patching not required")
	}

	slog.InfoContext(ctx, "successfully patched webhook configuration(s)")

	return nil
}

func (k *K8s) applyValidatingWebhook(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	valHook, err := k.clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting validating webhook: %w", err)
	}

	applyConfig := &admissionapplyv1.ValidatingWebhookConfigurationApplyConfiguration{
		Kind:       new("ValidatingWebhookConfiguration"),
		APIVersion: new("admissionregistration.k8s.io/v1"),
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
		FieldManager: fieldManager,
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed patching validating webhook: %w", err)
	}

	slog.DebugContext(ctx, "successfully applied validating webhook configuration")

	return nil
}

func (k *K8s) applyMutatingWebhook(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	mutHook, err := k.clientSet.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting mutating webhook: %w", err)
	}

	applyConfig := &admissionapplyv1.MutatingWebhookConfigurationApplyConfiguration{
		Kind:       new("MutatingWebhookConfiguration"),
		APIVersion: new("admissionregistration.k8s.io/v1"),
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
		FieldManager: fieldManager,
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed patching mutating webhook: %w", err)
	}

	slog.DebugContext(ctx, "successfully applied mutating webhook configuration")

	return nil
}
