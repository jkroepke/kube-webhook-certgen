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

type k8s struct {
	clientSet           kubernetes.Interface
	aggregatorClientSet clientset.Interface
}

func New(clientSet kubernetes.Interface, aggregatorClientSet clientset.Interface) (*k8s, error) {
	if clientSet == nil {
		return nil, errors.New("no kubernetes client given")
	}

	if aggregatorClientSet == nil {
		return nil, errors.New("no kubernetes aggregator client given")
	}

	return &k8s{
		clientSet:           clientSet,
		aggregatorClientSet: aggregatorClientSet,
	}, nil
}

type PatchOptions struct {
	ValidatingWebhookConfigurationName string
	MutatingWebhookConfigurationName   string
	APIServiceName                     string
	FailurePolicyType                  admissionregistrationv1.FailurePolicyType
	CABundle                           []byte
}

var ErrNoSecret = errors.New("no secret found")

// GetCaFromSecret will check for the presence of a secret. If it exists, will return the content of the
// "ca" from the secret, otherwise will return nil.
func (k8s *k8s) GetCaFromSecret(ctx context.Context, secretName, namespace string) ([]byte, error) {
	slog.DebugContext(ctx, fmt.Sprintf("getting secret '%s' in namespace '%s'", secretName, namespace))

	secret, err := k8s.clientSet.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, ErrNoSecret
		}

		return nil, fmt.Errorf("error getting secret: %w", err)
	}

	data := secret.Data["ca"]
	if data == nil {
		return nil, errors.New("got secret, but it did not contain a 'ca' key")
	}

	return data, nil
}

//nolint:cyclop
func (k8s *k8s) PatchObjects(ctx context.Context, options PatchOptions) error {
	patchMutating := options.MutatingWebhookConfigurationName != ""
	patchValidating := options.ValidatingWebhookConfigurationName != ""

	if !patchMutating && !patchValidating && options.FailurePolicyType != "" {
		return errors.New("failurePolicy specified, but no webhook will be patched")
	}

	if patchMutating && patchValidating &&
		options.MutatingWebhookConfigurationName != options.ValidatingWebhookConfigurationName {
		return errors.New("webhook names must be the same")
	}

	if options.APIServiceName != "" {
		slog.InfoContext(ctx, "patching APIService",
			slog.String("api_server", options.APIServiceName),
		)

		if err := k8s.patchAPIService(ctx, options.APIServiceName, options.CABundle); err != nil {
			// Intentionally don't wrap error here to preserve old behavior and be able to log both
			// original error and a message.
			return err
		}
	}

	webhookName := options.ValidatingWebhookConfigurationName
	if webhookName == "" {
		webhookName = options.MutatingWebhookConfigurationName
	}

	if patchMutating || patchValidating {
		return k8s.patchWebhookConfigurations(ctx, webhookName, options.CABundle, options.FailurePolicyType, patchMutating, patchValidating)
	}

	return nil
}

// SaveCertsToSecret saves the provided ca, cert and key into a secret in the specified namespace.
//
//nolint:revive
func (k8s *k8s) SaveCertsToSecret(ctx context.Context, secretName, namespace, certName, keyName string, ca, cert, key []byte) error {
	slog.DebugContext(ctx, fmt.Sprintf("saving to secret '%s' in namespace '%s'", secretName, namespace))
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Data: map[string][]byte{"ca": ca, certName: cert, keyName: key},
	}

	slog.DebugContext(ctx, "saving secret")

	_, err := k8s.clientSet.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating secret: %w", err)
	}

	slog.DebugContext(ctx, "saved secret")

	return nil
}

func (k8s *k8s) patchAPIService(ctx context.Context, objectName string, ca []byte) error {
	slog.InfoContext(ctx, "patching APIService",
		slog.String("api_server", objectName),
	)

	c := k8s.aggregatorClientSet.ApiregistrationV1().APIServices()

	apiService, err := c.Get(ctx, objectName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting APIService: %w", err)
	}

	apiService.Spec.CABundle = ca
	apiService.Spec.InsecureSkipTLSVerify = false

	if _, err := c.Update(ctx, apiService, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error patching APIService: %w", err)
	}

	slog.DebugContext(ctx, "patched APIService")

	return nil
}

// patchWebhookConfigurations will patch validatingWebhook and mutatingWebhook clientConfig configurations with
// the provided ca data. If failurePolicy is provided, patch all webhooks with this value.
func (k8s *k8s) patchWebhookConfigurations(
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
		if err := k8s.patchValidating(ctx, configurationName, ca, failurePolicy); err != nil {
			// Intentionally don't wrap error here to preserve old behavior and be able to log both original error and a message.
			return err
		}
	} else {
		slog.DebugContext(ctx, "validating hook patching not required")
	}

	if patchMutating {
		if err := k8s.patchMutating(ctx, configurationName, ca, failurePolicy); err != nil {
			// Intentionally don't wrap error here to preserve old behavior and be able to log both original error and a message.
			return err
		}
	} else {
		slog.DebugContext(ctx, "mutating hook patching not required")
	}

	slog.InfoContext(ctx, "Patched hook(s)")

	return nil
}

func (k8s *k8s) patchValidating(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	valHook, err := k8s.clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting validating webhook: %w", err)
	}

	kind := "ValidatingWebhookConfiguration"
	apiVersion := "admissionregistration.k8s.io/v1"

	applyConfig := &admissionapplyv1.ValidatingWebhookConfigurationApplyConfiguration{
		TypeMetaApplyConfiguration: meta.TypeMetaApplyConfiguration{
			Kind:       &kind,
			APIVersion: &apiVersion,
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

	if _, err = k8s.clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Apply(ctx, applyConfig, metav1.ApplyOptions{
		FieldManager: "kube-webhook-certgen",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed patching validating webhook: %w", err)
	}

	slog.DebugContext(ctx, "patched validating hook")

	return nil
}

func (k8s *k8s) patchMutating(ctx context.Context, configurationName string, ca []byte, failurePolicy admissionregistrationv1.FailurePolicyType) error {
	mutHook, err := k8s.clientSet.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, configurationName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting mutating webhook: %w", err)
	}

	kind := "MutatingWebhookConfigurations"
	apiVersion := "admissionregistration.k8s.io/v1"

	applyConfig := &admissionapplyv1.MutatingWebhookConfigurationApplyConfiguration{
		TypeMetaApplyConfiguration: meta.TypeMetaApplyConfiguration{
			Kind:       &kind,
			APIVersion: &apiVersion,
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

	for i := range mutHook.Webhooks {
		h := &mutHook.Webhooks[i]

		h.ClientConfig.CABundle = ca
		if failurePolicy != "" {
			h.FailurePolicy = &failurePolicy
		}
	}

	if _, err = k8s.clientSet.AdmissionregistrationV1().MutatingWebhookConfigurations().Apply(ctx, applyConfig, metav1.ApplyOptions{
		FieldManager: "kube-webhook-certgen",
		Force:        true,
	}); err != nil {
		return fmt.Errorf("failed patching mutating webhook: %w", err)
	}

	slog.DebugContext(ctx, "patched mutating hook")

	return nil
}
