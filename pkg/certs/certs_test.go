package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func handler(w http.ResponseWriter, _ *http.Request) {
	_, _ = fmt.Fprintf(w, "Hello World")
}

func TestCertificateCreation(t *testing.T) {
	t.Parallel()

	ca, cert, key, err := GenerateCerts("localhost")
	require.NoError(t, err)

	c, err := tls.X509KeyPair(cert, key)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(ca)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    caCertPool,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS12,
		},
	}

	ts := httptest.NewUnstartedServer(http.HandlerFunc(handler))
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{c},
		MinVersion:   tls.VersionTLS12,
	}

	ts.StartTLS()
	defer ts.Close()

	client := &http.Client{Transport: tr}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, nil)
	require.NoError(t, err)

	res, err := client.Do(req)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.StatusCode)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, []byte("Hello World"), body)
	require.NoError(t, res.Body.Close())
}
