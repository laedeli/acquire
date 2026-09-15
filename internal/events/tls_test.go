package events

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

// An empty cert dir (KAFKA_CERT_DIR set to "") is a plaintext listener: neither
// the consumer nor the producer uses TLS.
func TestPlaintextWithoutCertDir(t *testing.T) {
	cons, err := NewConsumer("kafka:9092", "", "tenant.", "tenant-acquire")
	if err != nil || cons == nil {
		t.Fatalf("consumer: %v, %v", cons, err)
	}
	if cons.tlsCfg != nil {
		t.Error("the consumer uses TLS without a cert dir")
	}

	prod, err := NewProducer([]string{"kafka:9092"}, "")
	if err != nil || prod == nil {
		t.Fatalf("producer: %v, %v", prod, err)
	}
	defer prod.Close()
	if tr, ok := prod.w.Transport.(*kafka.Transport); !ok || tr.TLS != nil {
		t.Error("the producer uses TLS without a cert dir")
	}
}

func TestMTLSWithCertDir(t *testing.T) {
	dir := writeCertDir(t)

	cons, err := NewConsumer("kafka:9093", dir, "tenant.", "tenant-acquire")
	if err != nil {
		t.Fatal(err)
	}
	if c := cons.tlsCfg; c == nil || len(c.Certificates) != 1 || c.RootCAs == nil {
		t.Errorf("consumer: want mTLS with the client certificate and the CA, got %v", c)
	}

	prod, err := NewProducer([]string{"kafka:9093"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	if tr, ok := prod.w.Transport.(*kafka.Transport); !ok || tr.TLS == nil || len(tr.TLS.Certificates) != 1 {
		t.Error("producer: want mTLS with the client certificate")
	}
}

// A cert dir without the certificate fails rather than quietly connecting in
// plaintext.
func TestCertDirWithoutCertificateFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewConsumer("kafka:9093", dir, "tenant.", "tenant-acquire"); err == nil {
		t.Error("consumer: want an error")
	}
	if _, err := NewProducer([]string{"kafka:9093"}, dir); err == nil {
		t.Error("producer: want an error")
	}
}

// writeCertDir writes a self-signed client certificate as user.crt, user.key
// and ca.crt, the layout of a KafkaUser secret.
func writeCertDir(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "acquire"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	dir := t.TempDir()
	for name, body := range map[string][]byte{
		"user.crt": cert,
		"user.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		"ca.crt":   cert,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
