[![CI](https://github.com/jkroepke/kube-webhook-certgen/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/jkroepke/kube-webhook-certgen/actions/workflows/ci.yaml)
[![GitHub license](https://img.shields.io/github/license/jkroepke/kube-webhook-certgen)](https://github.com/jkroepke/kube-webhook-certgen/blob/master/LICENSE.txt)
[![Current Release](https://img.shields.io/github/release/jkroepke/kube-webhook-certgen.svg?logo=github)](https://github.com/jkroepke/kube-webhook-certgen/releases/latest)
[![GitHub Repo stars](https://img.shields.io/github/stars/jkroepke/kube-webhook-certgen?style=flat&logo=github)](https://github.com/jkroepke/kube-webhook-certgen/stargazers)
[![Docker Pulls](https://img.shields.io/docker/pulls/jkroepke/kube-webhook-certgen?logo=docker)](https://hub.docker.com/r/jkroepke/kube-webhook-certgen)
[![Go Report Card](https://goreportcard.com/badge/github.com/jkroepke/kube-webhook-certgen)](https://goreportcard.com/report/github.com/jkroepke/kube-webhook-certgen)
[![codecov](https://codecov.io/gh/jkroepke/kube-webhook-certgen/graph/badge.svg?token=TJRPHF5BVX)](https://codecov.io/gh/jkroepke/kube-webhook-certgen)

# kube-webhook-certgen

⭐ Don't forget to star this repository! ⭐

# Kubernetes webhook certificate generator and patcher

**This is a copy/fork of the project existing in [jet/kube-webhook-certgen](https://github.com/jkroepke/kube-webhook-certgen/)**

## Overview
Generates a CA and leaf certificate with a long (100y) expiration, then patches [Kubernetes Admission Webhooks](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
by setting the `caBundle` field with the generated CA.
Can optionally patch the hooks `failurePolicy` setting - useful in cases where a single Helm chart needs to provision resources
and hooks at the same time as patching.

The utility works in two parts, optimized to work better with the Helm provisioning process that leverages pre-install and post-install hooks to execute this as a Kubernetes job.

## Security Considerations
This tool may not be adequate in all security environments. If a more complete solution is required, you may want to
seek alternatives such as [jetstack/cert-manager](https://github.com/jetstack/cert-manager)

## Command line options
```
Use this to create a ca and signed certificates and patch admission webhooks to allow for quick
                   installation and configuration of validating and admission webhooks.

Usage:
  kube-webhook-certgen [flags]
  kube-webhook-certgen [command]

Available Commands:
  create      Generate a ca and server cert+key and store the results in a secret 'secret-name' in 'namespace'
  help        Help about any command
  patch       Patch a validatingwebhookconfiguration and mutatingwebhookconfiguration 'webhook-name' by using the ca from 'secret-name' in 'namespace'
  version     Prints the CLI version information

Flags:
  -h, --help                help for kube-webhook-certgen
      --kubeconfig string   Path to kubeconfig file: e.g. ~/.kube/kind-config-kind
      --log-format string   Log format: text|json (default "text")
      --log-level string    Log level: error|warn|info|debug (default "info")
```

### Create
```
Generate a ca and server cert+key and store the results in a secret 'secret-name' in 'namespace'

Usage:
  kube-webhook-certgen create [flags]

Flags:
      --cert-name string     Name of cert file in the secret (default "cert")
  -h, --help                 help for create
      --host string          Comma-separated hostnames and IPs to generate a certificate for
      --key-name string      Name of key file in the secret (default "key")
      --namespace string     Namespace of the secret where certificate information will be written
      --secret-name string   Name of the secret where certificate information will be written

Global Flags:
      --kubeconfig string   Path to kubeconfig file: e.g. ~/.kube/kind-config-kind
      --log-format string   Log format: text|json (default "json")
      --log-level string    Log level: error|warn|info|debug (default "info")
```

### Patch
```
Patch a validatingwebhookconfiguration and mutatingwebhookconfiguration 'webhook-name' by using the ca from 'secret-name' in 'namespace'

Usage:
  kube-webhook-certgen patch [flags]

Flags:
  -h, --help                          help for patch
      --namespace string              Namespace of the secret where certificate information will be read from
      --patch-failure-policy string   If set, patch the webhooks with this failure policy. Valid options are Ignore or Fail
      --patch-mutating                If true, patch mutatingwebhookconfiguration (default true)
      --patch-validating              If true, patch validatingwebhookconfiguration (default true)
      --secret-name string            Name of the secret where certificate information will be read from
      --webhook-name string           Name of validatingwebhookconfiguration and mutatingwebhookconfiguration that will be updated

Global Flags:
      --kubeconfig string   Path to kubeconfig file: e.g. ~/.kube/kind-config-kind
      --log-format string   Log format: text|json (default "text")
      --log-level string    Log level: error|warn|info|debug (default "info")
```

## Known Users
- [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) helm chart

## Requirements

- Go 1.21+ (for building from source)
- Web server with syslog support (Nginx, Apache)
- Network connectivity between web server and access-log-exporter

## Contributing

Contributions welcome! Please read our [Code of Conduct](CODE_OF_CONDUCT.md) and submit pull requests to help improve the project.

## Related Projects

* [ingress-nginx/kube-webhook-certgen](https://github.com/kubernetes/ingress-nginx/tree/main/images/kube-webhook-certgen).
* [jet/kube-webhook-certgen](https://github.com/jet/kube-webhook-certgen)

## Copyright and license

© 2025 Jan-Otto Kröpke (jkroepke)

Licensed under the [Apache License, Version 2.0](LICENSE.txt).

## Open Source Sponsors

Thanks to all sponsors!

## Acknowledgements

Thanks to JetBrains IDEs for their support.

<table>
  <thead>
    <tr>
      <th><a href="https://www.jetbrains.com/?from=jkroepke">JetBrains IDEs</a></th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td>
        <p align="center">
          <a href="https://www.jetbrains.com/?from=jkroepke">
            <picture>
              <source srcset="https://www.jetbrains.com/company/brand/img/logo_jb_dos_3.svg" media="(prefers-color-scheme: dark)">
              <img src="https://resources.jetbrains.com/storage/products/company/brand/logos/jetbrains.svg" style="height: 50px">
            </picture>
          </a>
        </p>
      </td>
    </tr>
  </tbody>
</table>
